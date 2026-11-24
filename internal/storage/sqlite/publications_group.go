package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"slices"
	"strings"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

var _ publication.TemplateGroupStore = (*Publications)(nil)

func (p *Publications) ReadTemplateGroup(ctx context.Context, id string) (*publication.TemplateGroupInputs, error) {
	if !validPublicationID(id) {
		return nil, publication.ErrInvalidState
	}
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, operationError("read publication group", ctx.Err())
	}
	defer tx.Rollback()

	draft, err := readPublicationTemplate(ctx, tx, readTemplateDraft, id)
	if err != nil {
		return nil, err
	}
	if draft == nil {
		return nil, publication.ErrSourceMissing
	}
	accepted, err := readPublicationTemplate(ctx, tx, readAcceptedTemplate, id)
	if err != nil {
		return nil, err
	}
	if accepted != nil && accepted.Version > draft.Version {
		return nil, publication.ErrStoredState
	}
	if accepted != nil && accepted.Version == draft.Version {
		if err := checkSameSharedVersion(ctx, tx, id, draft.Version); err != nil {
			return nil, err
		}
	}
	ids, err := readGroupRouterIDs(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	inputs := &publication.TemplateGroupInputs{
		TemplateDraft: draft,
		Accepted:      accepted,
		Members:       make([]publication.TemplateGroupMember, 0, len(ids)),
	}
	for _, routerID := range ids {
		active, err := readActivePublication(ctx, tx, routerID)
		if err != nil {
			return nil, err
		}
		desired, err := readDesiredRevision(ctx, tx, routerID)
		if err != nil {
			return nil, err
		}
		if active == nil || desired == nil || active.Draft.TemplateID != id || !validActiveSource(active, accepted) {
			return nil, publication.ErrStoredState
		}
		inputs.Members = append(inputs.Members, publication.TemplateGroupMember{Active: *active, Desired: *desired})
	}
	if err := tx.Commit(); err != nil {
		return nil, operationError("finish reading publication group", ctx.Err())
	}
	return inputs, nil
}

func readGroupRouterIDs(ctx context.Context, tx *sql.Tx, id string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT router_id FROM active_router_publications
		WHERE template_id = ? ORDER BY router_id COLLATE BINARY`, id)
	if err != nil {
		return nil, operationError("read publication group members", ctx.Err())
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var routerID string
		if err := rows.Scan(&routerID); err != nil {
			return nil, operationError("read publication group member", ctx.Err())
		}
		ids = append(ids, routerID)
	}
	if err := rows.Err(); err != nil {
		return nil, operationError("read publication group members", ctx.Err())
	}
	return ids, nil
}

func (p *Publications) CommitTemplateGroup(ctx context.Context, plan publication.TemplateGroupPlan) (*publication.TemplateGroupResult, error) {
	members, err := orderedGroupPlan(plan)
	if err != nil {
		return nil, err
	}
	edit, files, err := encodeGroupWrites(plan, members)
	if err != nil {
		return nil, err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, operationError("write publication group", ctx.Err())
	}
	defer tx.Rollback()
	if err := checkGroupExpectations(ctx, tx, plan, members); err != nil {
		return nil, err
	}
	accepted, err := acceptGroupTemplate(ctx, tx, plan, edit)
	if err != nil {
		return nil, err
	}
	result := &publication.TemplateGroupResult{
		TemplateID: plan.TemplateID, Version: accepted.Version,
		Routers: make([]publication.GroupRouterResult, 0, len(members)),
	}
	for i, member := range members {
		if err := writeGroupOrder(ctx, tx, accepted, member); err != nil {
			return nil, err
		}
		e := member.Expected
		outcome := publication.GroupRouterResult{RouterID: e.RouterID, Revision: e.DesiredNumber}
		if member.NewRevision != nil {
			outcome.Changed = true
			outcome.Revision++
			if err := writePublicationRow(ctx, tx, `INSERT INTO desired_router_revisions
				(router_id, revision, profile, files) VALUES (?,?,?,?)`, e.RouterID,
				outcome.Revision, member.NewRevision.Profile, files[i]); err != nil {
				return nil, err
			}
		}
		result.Routers = append(result.Routers, outcome)
	}
	if err := tx.Commit(); err != nil {
		return nil, operationError("commit publication group", ctx.Err())
	}
	return result, nil
}

func encodeGroupWrites(plan publication.TemplateGroupPlan, members []publication.GroupRouterPlan) ([]byte, [][]byte, error) {
	var edit []byte
	if plan.Edit != nil {
		if plan.TemplateDraftVersion == math.MaxInt64 {
			return nil, nil, publication.ErrVersionExhausted
		}
		if issues := configuration.ValidateTemplateDraft(*plan.Edit); len(issues) != 0 {
			return nil, nil, publication.ErrInvalidState
		}
		var err error
		edit, err = encodeTemplate(*plan.Edit)
		if err != nil {
			return nil, nil, publication.ErrInvalidState
		}
	}
	files := make([][]byte, len(members))
	for i, member := range members {
		if member.NewRevision != nil && member.Expected.DesiredNumber == math.MaxInt64 {
			return nil, nil, publication.ErrVersionExhausted
		}
		var err error
		files[i], err = encodeVerifiedFiles(member.NewRevision)
		if err != nil {
			return nil, nil, err
		}
	}
	return edit, files, nil
}

func acceptGroupTemplate(ctx context.Context, tx *sql.Tx, plan publication.TemplateGroupPlan, edit []byte) (*templates.Snapshot, error) {
	version := plan.TemplateDraftVersion
	if edit != nil {
		version++
		if err := writePublicationRow(ctx, tx, `UPDATE templates SET draft = ?, version = ?
			WHERE id = ? AND version = ?`, edit, version, plan.TemplateID, plan.TemplateDraftVersion); err != nil {
			return nil, err
		}
	}
	if plan.AcceptedVersion == 0 && len(plan.Members) != 0 {
		return nil, publication.ErrStoredState
	}
	if version == plan.AcceptedVersion {
		if err := checkSameSharedVersion(ctx, tx, plan.TemplateID, version); err != nil {
			return nil, err
		}
	} else {
		if err := writePublicationRow(ctx, tx, `INSERT INTO accepted_templates (template_id, version, draft)
			SELECT id, version, draft FROM templates WHERE id = ? AND version = ?`, plan.TemplateID, version); err != nil {
			return nil, err
		}
	}
	snapshot, err := readPublicationTemplate(ctx, tx, readAcceptedTemplate, plan.TemplateID)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.Version != version {
		return nil, publication.ErrStoredState
	}
	return snapshot, nil
}

func checkSameSharedVersion(ctx context.Context, tx *sql.Tx, id string, version int64) error {
	var same bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM templates AS source JOIN accepted_templates AS accepted
			ON accepted.template_id = source.id AND accepted.version = source.version
		WHERE source.id = ? AND source.version = ? AND source.draft = accepted.draft
	)`, id, version).Scan(&same)
	if err != nil {
		return operationError("compare shared template snapshot", ctx.Err())
	}
	if !same {
		return publication.ErrStoredState
	}
	return nil
}

func writeGroupOrder(ctx context.Context, tx *sql.Tx, accepted *templates.Snapshot, member publication.GroupRouterPlan) error {
	active, err := readActivePublication(ctx, tx, member.Expected.RouterID)
	if err != nil {
		return err
	}
	if active == nil || active.Draft.TemplateID != accepted.ID {
		return publication.ErrStoredState
	}
	oldOrder := active.Draft.PersonalOrder
	if (oldOrder == nil) != (member.PersonalOrder == nil) {
		return publication.ErrInvalidState
	}
	active.Draft.PersonalOrder = member.PersonalOrder
	basis := member.OrderBasis
	if !validStoredBasis(active.Draft, basis) || (basis != nil && !basisMatchesTemplate(basis, accepted)) ||
		routersettings.ValidateDraft(active.Draft) != nil {
		return publication.ErrInvalidState
	}
	if slices.Equal(oldOrder, member.PersonalOrder) &&
		sameGroupOrderBasis(active.OrderBasis, basis) {
		return nil
	}
	if active.Generation == math.MaxInt64 {
		return publication.ErrVersionExhausted
	}
	raw, err := json.Marshal(encodeRouterDraft(active.Draft))
	if err != nil {
		return publication.ErrInvalidState
	}
	var rawBasis []byte
	if basis != nil {
		rawBasis, err = json.Marshal(basis)
		if err != nil {
			return publication.ErrInvalidState
		}
	}
	return writePublicationRow(ctx, tx, `UPDATE active_router_publications SET generation = ?, settings = ?, order_basis = ?
		WHERE router_id = ? AND template_id = ?`, active.Generation+1, raw, rawBasis, active.Draft.ID, accepted.ID)
}

func sameGroupOrderBasis(a, b *publication.OrderBasis) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.TemplateID == b.TemplateID && a.TemplateVersion == b.TemplateVersion && slices.Equal(a.SharedRuleIDs, b.SharedRuleIDs)
}

func orderedGroupPlan(plan publication.TemplateGroupPlan) ([]publication.GroupRouterPlan, error) {
	if !validPublicationID(plan.TemplateID) || plan.TemplateDraftVersion < 1 ||
		plan.AcceptedVersion < 0 || plan.AcceptedVersion > plan.TemplateDraftVersion ||
		(plan.Edit != nil && plan.Edit.ID != plan.TemplateID) {
		return nil, publication.ErrInvalidState
	}
	members := slices.Clone(plan.Members)
	slices.SortFunc(members, func(a, b publication.GroupRouterPlan) int {
		return strings.Compare(a.Expected.RouterID, b.Expected.RouterID)
	})
	for i, member := range members {
		e := member.Expected
		if !validPublicationID(e.RouterID) || e.ActiveGeneration < 1 || e.DesiredNumber < 1 ||
			(i > 0 && e.RouterID == members[i-1].Expected.RouterID) {
			return nil, publication.ErrInvalidState
		}
	}
	return members, nil
}

func checkGroupExpectations(ctx context.Context, tx *sql.Tx, plan publication.TemplateGroupPlan, members []publication.GroupRouterPlan) error {
	ids, err := readGroupRouterIDs(ctx, tx, plan.TemplateID)
	if err != nil {
		return err
	}
	if len(ids) != len(members) {
		return publication.ErrConflict
	}
	for i, id := range ids {
		if id != members[i].Expected.RouterID {
			return publication.ErrConflict
		}
	}
	if err := comparePublicationVersion(ctx, tx, "SELECT coalesce(max(version),0) FROM templates WHERE id = ?",
		plan.TemplateID, plan.TemplateDraftVersion); err != nil {
		return err
	}
	if err := comparePublicationVersion(ctx, tx, "SELECT coalesce(max(version),0) FROM accepted_templates WHERE template_id = ?",
		plan.TemplateID, plan.AcceptedVersion); err != nil {
		return err
	}
	for _, member := range members {
		e := member.Expected
		if err := comparePublicationVersion(ctx, tx,
			"SELECT coalesce(max(generation),0) FROM active_router_publications WHERE router_id = ?", e.RouterID, e.ActiveGeneration); err != nil {
			return err
		}
		if err := comparePublicationVersion(ctx, tx, "SELECT coalesce(max(revision),0) FROM desired_router_revisions WHERE router_id = ?",
			e.RouterID, e.DesiredNumber); err != nil {
			return err
		}
	}
	return nil
}
