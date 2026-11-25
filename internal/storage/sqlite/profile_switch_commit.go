package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
)

func (p *Publications) CommitProfileSwitch(ctx context.Context, plan publication.ProfileSwitchPlan) (int64, error) {
	return p.commitProfileSwitch(ctx, plan, nil)
}

func (p *Publications) commitProfileSwitch(ctx context.Context, plan publication.ProfileSwitchPlan, guard publicationGuard) (int64, error) {
	expected := plan.Expected
	if !validPublicationID(expected.SourceTemplateID) || expected.SourceTemplateID == expected.TargetTemplateID ||
		expected.SourceAcceptedVersion < 1 || expected.SlotGeneration < 0 || expected.TargetDraftGeneration < 0 {
		return 0, publication.ErrInvalidState
	}
	if expected.SlotGeneration == math.MaxInt64 {
		return 0, publication.ErrVersionExhausted
	}
	encoded, err := encodePublicationPlan(publication.CommitPlan{
		Expected: publication.Expectations{
			RouterID: expected.RouterID, TemplateID: expected.TargetTemplateID, AcceptedVersion: expected.TargetAcceptedVersion,
			ActiveGeneration: expected.ActiveGeneration, DesiredNumber: expected.DesiredNumber,
		},
		Settings: plan.Settings, OrderBasis: plan.OrderBasis, NewRevision: plan.NewRevision,
	})
	if err != nil {
		return 0, err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, operationError("write profile switch", ctx.Err())
	}
	defer tx.Rollback()
	if guard != nil {
		if err := guard(ctx, tx); err != nil {
			return 0, err
		}
	}
	rows, err := readProfileSwitchRows(ctx, tx, expected.RouterID, expected.TargetTemplateID)
	if errors.Is(err, publication.ErrSourceMissing) {
		return 0, publication.ErrConflict
	}
	if err != nil {
		return 0, err
	}
	if rows.inputs.AlreadyActive || rows.inputs.Basis != expected {
		return 0, publication.ErrConflict
	}
	if plan.OrderBasis != nil && !basisMatchesTemplate(plan.OrderBasis, rows.inputs.Target) {
		return 0, publication.ErrInvalidState
	}
	if err := exchangeProfileDrafts(ctx, tx, rows, encoded); err != nil {
		return 0, err
	}
	if err := writePublicationRow(ctx, tx, `UPDATE active_router_publications
		SET template_id=?,generation=generation+1,settings=?,order_basis=?
		WHERE router_id=? AND template_id=? AND generation=?`, expected.TargetTemplateID, encoded.settings, encoded.orderBasis,
		expected.RouterID, expected.SourceTemplateID, expected.ActiveGeneration); err != nil {
		return 0, err
	}
	number := expected.DesiredNumber
	if plan.NewRevision != nil {
		number++
		if err := writePublicationRow(ctx, tx, `INSERT INTO desired_router_revisions
			(router_id,revision,profile,files) VALUES (?,?,?,?)`, expected.RouterID, number, plan.NewRevision.Profile, encoded.files); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, operationError("commit profile switch", ctx.Err())
	}
	if plan.NewRevision != nil {
		p.signal(expected.RouterID)
	}
	return number, nil
}

func exchangeProfileDrafts(ctx context.Context, tx *sql.Tx, rows profileSwitchRows, encoded encodedPublication) error {
	b := rows.inputs.Basis
	source := rows.inputs.Current
	previous := publication.VersionedRouterDraft{Draft: source.Draft, OrderBasis: source.OrderBasis, Generation: source.Generation}
	if rows.slot != nil && rows.slot.Draft.TemplateID == b.SourceTemplateID {
		previous = *rows.slot
		previous.OrderBasis = publication.EditableRuleOrderBasis(rows.slot, source)
	}
	if err := insertInactiveProfileDraft(ctx, tx, previous); err != nil {
		return err
	}
	if rows.slot != nil && rows.slot.Draft.TemplateID != b.SourceTemplateID && rows.slot.Draft.TemplateID != b.TargetTemplateID {
		if err := insertInactiveProfileDraft(ctx, tx, *rows.slot); err != nil {
			return err
		}
	}
	if rows.savedTarget != nil {
		if err := writePublicationRow(ctx, tx, `DELETE FROM inactive_router_profile_drafts
			WHERE router_id=? AND template_id=? AND generation=?`, b.RouterID, b.TargetTemplateID, rows.savedTarget.Generation); err != nil {
			return err
		}
	}
	if rows.slot == nil {
		return writePublicationRow(ctx, tx, `INSERT INTO router_settings (id,snapshot,order_basis) VALUES (?,?,?)`,
			b.RouterID, encoded.settings, encoded.orderBasis)
	}
	return writePublicationRow(ctx, tx, `UPDATE router_settings SET snapshot=?,order_basis=?,generation=generation+1
		WHERE id=? AND generation=?`, encoded.settings, encoded.orderBasis, b.RouterID, b.SlotGeneration)
}

func insertInactiveProfileDraft(ctx context.Context, tx *sql.Tx, draft publication.VersionedRouterDraft) error {
	raw, err := json.Marshal(encodeRouterDraft(draft.Draft))
	if err != nil {
		return publication.ErrStoredState
	}
	var rawBasis []byte
	if draft.OrderBasis != nil {
		rawBasis, err = json.Marshal(draft.OrderBasis)
		if err != nil {
			return publication.ErrStoredState
		}
	}
	return writePublicationRow(ctx, tx, `INSERT INTO inactive_router_profile_drafts
		(router_id,template_id,generation,snapshot,order_basis) VALUES (?,?,?,?,?)`,
		draft.Draft.ID, draft.Draft.TemplateID, draft.Generation, raw, rawBasis)
}
