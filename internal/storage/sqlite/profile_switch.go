package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

const createInactiveProfileDrafts = `CREATE TABLE inactive_router_profile_drafts (
	router_id TEXT NOT NULL COLLATE BINARY,
	template_id TEXT NOT NULL COLLATE BINARY,
	generation INTEGER NOT NULL CHECK (typeof(generation) = 'integer' AND generation > 0),
	snapshot BLOB NOT NULL,
	order_basis BLOB,
	PRIMARY KEY (router_id, template_id)
) WITHOUT ROWID`

var _ publication.ProfileSwitchStore = (*Publications)(nil)

type profileSwitchRows struct {
	inputs      publication.ProfileSwitchInputs
	slot        *publication.VersionedRouterDraft
	savedTarget *publication.VersionedRouterDraft
}

func (p *Publications) ReadProfileSwitch(ctx context.Context, routerID, targetTemplateID string) (*publication.ProfileSwitchInputs, error) {
	return p.readProfileSwitch(ctx, routerID, targetTemplateID, nil)
}

func (p *Publications) readProfileSwitch(ctx context.Context, routerID, targetTemplateID string, guard publicationGuard) (*publication.ProfileSwitchInputs, error) {
	if !validPublicationID(routerID) || !validPublicationID(targetTemplateID) {
		return nil, publication.ErrInvalidState
	}
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, operationError("read profile switch", ctx.Err())
	}
	defer tx.Rollback()
	if guard != nil {
		if err := guard(ctx, tx); err != nil {
			return nil, err
		}
	}
	rows, err := readProfileSwitchRows(ctx, tx, routerID, targetTemplateID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, operationError("finish reading profile switch", ctx.Err())
	}
	return &rows.inputs, nil
}

func readProfileSwitchRows(ctx context.Context, tx *sql.Tx, routerID, targetID string) (profileSwitchRows, error) {
	active, err := readActivePublication(ctx, tx, routerID)
	if err != nil {
		return profileSwitchRows{}, err
	}
	if active == nil {
		return profileSwitchRows{}, publication.ErrSourceMissing
	}
	sourceID := active.Draft.TemplateID
	source, err := readPublicationTemplate(ctx, tx, readAcceptedTemplate, sourceID)
	if err != nil {
		return profileSwitchRows{}, err
	}
	if !validActiveSource(active, source) {
		return profileSwitchRows{}, publication.ErrStoredState
	}
	target := source
	if targetID != sourceID {
		target, err = readPublicationTemplate(ctx, tx, readAcceptedTemplate, targetID)
		if err != nil {
			return profileSwitchRows{}, err
		}
		if target == nil {
			return profileSwitchRows{}, publication.ErrSourceMissing
		}
	}
	desired, err := readDesiredRevision(ctx, tx, routerID)
	if err != nil {
		return profileSwitchRows{}, err
	}
	if desired == nil {
		return profileSwitchRows{}, publication.ErrStoredState
	}
	slot, err := readPublicationRouterDraft(ctx, tx, routerID)
	if err != nil {
		return profileSwitchRows{}, err
	}
	inputs := publication.ProfileSwitchInputs{
		Basis: publication.ProfileSwitchBasis{
			RouterID: routerID, SourceTemplateID: sourceID, TargetTemplateID: targetID,
			SourceAcceptedVersion: source.Version, TargetAcceptedVersion: target.Version,
			ActiveGeneration: active.Generation, DesiredNumber: desired.Number,
		},
		Current: active, Target: target, Desired: desired, AlreadyActive: targetID == sourceID,
	}
	if slot != nil {
		inputs.Basis.SlotTemplateID = slot.Draft.TemplateID
		inputs.Basis.SlotGeneration = slot.Generation
	}
	rows := profileSwitchRows{inputs: inputs, slot: slot}
	if inputs.AlreadyActive {
		return rows, nil
	}
	// The active source and the current C04 slot must not compete with an
	// inactive snapshot for either of their identities.
	if err := requireInactiveProfilesAbsent(ctx, tx, routerID, sourceID, inputs.Basis.SlotTemplateID); err != nil {
		return profileSwitchRows{}, err
	}
	if slot != nil && slot.Draft.TemplateID == targetID {
		rows.inputs.TargetDraft = slot
	} else {
		rows.savedTarget, err = readInactiveProfileDraft(ctx, tx, routerID, targetID)
		if err != nil {
			return profileSwitchRows{}, err
		}
		rows.inputs.TargetDraft = rows.savedTarget
	}
	if rows.inputs.TargetDraft != nil {
		rows.inputs.Basis.TargetDraftGeneration = rows.inputs.TargetDraft.Generation
	}
	rows.inputs.Exclusions, err = publicationExclusions(ctx, tx, targetID, &target.Version)
	if err != nil {
		return profileSwitchRows{}, err
	}
	return rows, nil
}

func requireInactiveProfilesAbsent(ctx context.Context, tx *sql.Tx, routerID, sourceID, slotID string) error {
	var duplicate bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM inactive_router_profile_drafts WHERE router_id=? AND template_id IN (?,?)
	)`, routerID, sourceID, slotID).Scan(&duplicate)
	if err != nil {
		return operationError("compare inactive router scope", ctx.Err())
	}
	if duplicate {
		return publication.ErrStoredState
	}
	return nil
}

func readInactiveProfileDraft(ctx context.Context, tx *sql.Tx, id, templateID string) (*publication.VersionedRouterDraft, error) {
	var raw, rawBasis []byte
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT snapshot,generation,order_basis FROM inactive_router_profile_drafts
		WHERE router_id=? AND template_id=?`, id, templateID).Scan(&raw, &generation, &rawBasis)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, operationError("read inactive router draft", ctx.Err())
	}
	draft, err := decodeRouterDraft(id, raw)
	if err != nil || generation < 1 || draft.TemplateID != templateID || routersettings.ValidateDraft(draft) != nil {
		return nil, publication.ErrStoredState
	}
	var basis *publication.OrderBasis
	// A legacy C04 draft may contain personal order without its provenance.
	// Retain the draft; publication requires a valid basis for its use.
	if rawBasis != nil {
		basis, err = decodeStoredOrderBasis(draft, rawBasis)
		if err != nil {
			return nil, err
		}
	}
	return &publication.VersionedRouterDraft{Draft: draft, Generation: generation, OrderBasis: basis}, nil
}
