package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type WebRouterDrafts struct{ db *sql.DB }

func NewWebRouterDrafts(db *sql.DB) WebRouterDrafts { return WebRouterDrafts{db: db} }

func (w WebRouterDrafts) Read(ctx context.Context, digest [32]byte, clock func() time.Time, id string) (webadmin.RouterDraftSnapshot, error) {
	var result webadmin.RouterDraftSnapshot
	if !validPublicationID(id) {
		return result, webadmin.ErrRouterDraftMissing
	}
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err := requireActiveWebSession(ctx, tx, activeWebSessionArgs(digest, clock())); err != nil {
		return result, err
	}
	basis, err := readVisibleRouterDraftBasis(ctx, tx, id)
	if err != nil {
		return result, err
	}
	active, err := readActivePublication(ctx, tx, id)
	if err != nil {
		return result, err
	}
	accepted, err := readPublicationTemplate(ctx, tx, readAcceptedTemplate, basis.TemplateID)
	if err != nil {
		return result, err
	}
	draft, err := readPublicationRouterDraft(ctx, tx, id)
	if err != nil {
		return result, err
	}
	if active == nil || !validActiveSource(active, accepted) ||
		(draft == nil) != (basis.DraftGeneration == 0) {
		return result, publication.ErrStoredState
	}
	name := active.Draft.Name
	if name == "" {
		name = id
	}
	editable := active.Draft
	if draft != nil {
		editable = draft.Draft
	}
	source, err := publicationExclusions(ctx, tx, basis.TemplateID, &basis.AcceptedVersion)
	if err != nil {
		return result, err
	}
	return webadmin.RouterDraftSnapshot{Exclusions: source, OrderBasis: publication.EditableRuleOrderBasis(draft, active), Basis: basis, Name: name, Accepted: accepted, Editable: editable}, nil
}

func readVisibleRouterDraftBasis(ctx context.Context, tx *sql.Tx, id string) (webadmin.RouterDraftBasis, error) {
	basis := webadmin.RouterDraftBasis{RouterID: id}
	err := tx.QueryRowContext(ctx, `SELECT active.template_id, active.generation,
		coalesce((SELECT max(version) FROM accepted_templates WHERE template_id=active.template_id),0),
		coalesce((SELECT generation FROM router_settings WHERE id=active.router_id),0)`+
		visibleRouterSource+` AND active.router_id = ?`, id).
		Scan(&basis.TemplateID, &basis.ActiveGeneration, &basis.AcceptedVersion, &basis.DraftGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return webadmin.RouterDraftBasis{}, webadmin.ErrRouterDraftMissing
	}
	if err != nil {
		return webadmin.RouterDraftBasis{}, err
	}
	if !validPublicationID(basis.TemplateID) || basis.ActiveGeneration < 1 || basis.AcceptedVersion < 1 || basis.DraftGeneration < 0 {
		return webadmin.RouterDraftBasis{}, publication.ErrStoredState
	}
	return basis, nil
}

func (w WebRouterDrafts) Write(ctx context.Context, digest [32]byte, clock func() time.Time, expected webadmin.RouterDraftBasis, draft routersettings.Draft) (int64, error) {
	return w.write(ctx, digest, clock, expected, draft, nil)
}

func (w WebRouterDrafts) WriteOrder(ctx context.Context, digest [32]byte, clock func() time.Time, expected webadmin.RouterDraftBasis, draft routersettings.Draft, basis *publication.OrderBasis) (int64, error) {
	if basis == nil || !validStoredBasis(draft, basis) {
		return 0, routersettings.ErrInvalidDraft
	}
	return w.write(ctx, digest, clock, expected, draft, basis)
}

func (w WebRouterDrafts) write(ctx context.Context, digest [32]byte, clock func() time.Time, expected webadmin.RouterDraftBasis, draft routersettings.Draft, basis *publication.OrderBasis) (int64, error) {
	if draft.ID != expected.RouterID || draft.TemplateID != expected.TemplateID {
		return 0, webadmin.ErrRouterDraftConflict
	}
	if err := routersettings.ValidateDraft(draft); err != nil {
		return 0, err
	}
	raw, err := json.Marshal(encodeRouterDraft(draft))
	if err != nil {
		return 0, err
	}
	var rawBasis []byte
	if basis != nil {
		rawBasis, err = json.Marshal(basis)
		if err != nil {
			return 0, err
		}
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := clock()
	if err := touchActiveWebSession(ctx, tx, now, activeWebSessionArgs(digest, now)); err != nil {
		return 0, err
	}
	current, err := readVisibleRouterDraftBasis(ctx, tx, expected.RouterID)
	if err != nil {
		return 0, err
	}
	if current != expected || current.DraftGeneration == math.MaxInt64 {
		return 0, webadmin.ErrRouterDraftConflict
	}
	if basis != nil {
		accepted, err := readPublicationTemplate(ctx, tx, readAcceptedTemplate, current.TemplateID)
		if err != nil {
			return 0, err
		}
		if !basisMatchesTemplate(basis, accepted) {
			return 0, webadmin.ErrRouterDraftConflict
		}
	}
	if current.DraftGeneration == 0 {
		err = writePublicationRow(ctx, tx, `INSERT INTO router_settings (id, snapshot, order_basis)
			VALUES (?, ?, coalesce(?, (SELECT order_basis FROM active_router_publications WHERE router_id = ?)))`,
			draft.ID, raw, rawBasis, draft.ID)
	} else {
		var n int64
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE router_settings
			SET snapshot = ?, generation = generation + 1, order_basis = coalesce(?, order_basis)
			WHERE id = ? AND generation = ? AND json_extract(CAST(snapshot AS TEXT), '$.TemplateID') = ?`,
			raw, rawBasis, draft.ID, current.DraftGeneration, current.TemplateID)
		if err == nil {
			n, err = result.RowsAffected()
			if err == nil && n != 1 {
				return 0, webadmin.ErrRouterDraftForeign
			}
		}
	}
	if err != nil {
		return 0, operationError("write scoped router draft", ctx.Err())
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return current.DraftGeneration + 1, nil
}
