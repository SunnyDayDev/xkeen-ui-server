package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

type encodedPublication struct {
	settings, orderBasis, files []byte
}

func (p *Publications) Commit(ctx context.Context, plan publication.CommitPlan) (int64, error) {
	return p.commit(ctx, plan, nil)
}

func (p *Publications) commit(ctx context.Context, plan publication.CommitPlan, guard publicationGuard) (int64, error) {
	encoded, err := encodePublicationPlan(plan)
	if err != nil {
		return 0, err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, operationError("write publication", ctx.Err())
	}
	defer tx.Rollback()
	if guard != nil {
		if err := guard(ctx, tx); err != nil {
			return 0, err
		}
	}

	if err := checkPublicationExpectations(ctx, tx, plan); err != nil {
		return 0, err
	}
	if err := checkPublicationOrderBasis(ctx, tx, plan); err != nil {
		return 0, err
	}
	expected := plan.Expected
	if plan.AcceptInitialTemplate {
		if err := writePublicationRow(ctx, tx, `INSERT INTO accepted_templates (template_id, version, draft)
			SELECT id, version, draft FROM templates WHERE id = ? AND version = ?`,
			expected.TemplateID, *expected.TemplateDraftVersion); err != nil {
			return 0, err
		}
	}
	if plan.FirstConfirmation {
		err = writePublicationRow(ctx, tx, `INSERT INTO active_router_publications
			(router_id, template_id, generation, settings, order_basis) VALUES (?,?,?,?,?)`,
			expected.RouterID, expected.TemplateID, 1, encoded.settings, encoded.orderBasis)
	} else {
		err = writePublicationRow(ctx, tx, `UPDATE active_router_publications
			SET generation = ?, settings = ?, order_basis = ? WHERE router_id = ? AND template_id = ?`,
			expected.ActiveGeneration+1, encoded.settings, encoded.orderBasis, expected.RouterID, expected.TemplateID)
	}
	if err != nil {
		return 0, err
	}

	number := expected.DesiredNumber
	if plan.NewRevision != nil {
		number++
		if err := writePublicationRow(ctx, tx, `INSERT INTO desired_router_revisions
			(router_id, revision, profile, files) VALUES (?,?,?,?)`,
			expected.RouterID, number, plan.NewRevision.Profile, encoded.files); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, operationError("commit publication", ctx.Err())
	}
	if plan.NewRevision != nil {
		p.signal(expected.RouterID)
	}
	return number, nil
}

func encodePublicationPlan(plan publication.CommitPlan) (encodedPublication, error) {
	expected := plan.Expected
	if expected.RouterID != plan.Settings.ID || expected.TemplateID != plan.Settings.TemplateID ||
		routersettings.ValidateDraft(plan.Settings) != nil || !validStoredBasis(plan.Settings, plan.OrderBasis) ||
		(expected.TemplateDraftVersion != nil && *expected.TemplateDraftVersion < 0) ||
		(expected.RouterDraftGeneration != nil && *expected.RouterDraftGeneration < 0) {
		return encodedPublication{}, publication.ErrInvalidState
	}
	if !validCommitMode(plan) {
		return encodedPublication{}, publication.ErrInvalidState
	}
	if expected.ActiveGeneration == math.MaxInt64 || (plan.NewRevision != nil && expected.DesiredNumber == math.MaxInt64) {
		return encodedPublication{}, publication.ErrVersionExhausted
	}
	settings, err := json.Marshal(encodeRouterDraft(plan.Settings))
	if err != nil {
		return encodedPublication{}, publication.ErrInvalidState
	}
	result := encodedPublication{settings: settings}
	if plan.OrderBasis != nil {
		result.orderBasis, err = json.Marshal(plan.OrderBasis)
		if err != nil {
			return encodedPublication{}, publication.ErrInvalidState
		}
	}
	result.files, err = encodeVerifiedFiles(plan.NewRevision)
	if err != nil {
		return encodedPublication{}, err
	}
	return result, nil
}

func encodeVerifiedFiles(catalog *publication.VerifiedFiles) ([]byte, error) {
	if catalog == nil {
		return nil, nil
	}
	records := make([]publicationFileRecord, len(catalog.Files))
	for i, file := range catalog.Files {
		records[i] = publicationFileRecord{Name: file.Name, Bytes: file.Content}
	}
	if !validPublicationID(catalog.Profile) || !validFileRecords(records) {
		return nil, publication.ErrInvalidState
	}
	raw, err := json.Marshal(records)
	if err != nil {
		return nil, publication.ErrInvalidState
	}
	return raw, nil
}

func validCommitMode(plan publication.CommitPlan) bool {
	expected := plan.Expected
	if !plan.FirstConfirmation {
		return !plan.AcceptInitialTemplate && expected.AcceptedVersion > 0 &&
			expected.ActiveGeneration > 0 && expected.DesiredNumber > 0
	}
	if expected.ActiveGeneration != 0 || expected.DesiredNumber != 0 || plan.NewRevision == nil {
		return false
	}
	if !plan.AcceptInitialTemplate {
		return expected.AcceptedVersion > 0
	}
	return expected.AcceptedVersion == 0 && expected.TemplateDraftVersion != nil && *expected.TemplateDraftVersion > 0
}

func checkPublicationExpectations(ctx context.Context, tx *sql.Tx, plan publication.CommitPlan) error {
	expected := plan.Expected
	var templateID string
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT template_id, generation FROM active_router_publications
		WHERE router_id = ?`, expected.RouterID).Scan(&templateID, &generation)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if !plan.FirstConfirmation {
			return publication.ErrConflict
		}
	case err != nil:
		return operationError("compare active publication", ctx.Err())
	case plan.FirstConfirmation || templateID != expected.TemplateID || generation != expected.ActiveGeneration:
		return publication.ErrConflict
	}
	if err := comparePublicationVersion(ctx, tx,
		"SELECT coalesce(max(version),0) FROM accepted_templates WHERE template_id = ?",
		expected.TemplateID, expected.AcceptedVersion); err != nil {
		return err
	}
	if err := comparePublicationVersion(ctx, tx,
		"SELECT coalesce(max(revision),0) FROM desired_router_revisions WHERE router_id = ?",
		expected.RouterID, expected.DesiredNumber); err != nil {
		return err
	}
	if expected.TemplateDraftVersion != nil {
		if err := comparePublicationVersion(ctx, tx, "SELECT coalesce(max(version),0) FROM templates WHERE id = ?",
			expected.TemplateID, *expected.TemplateDraftVersion); err != nil {
			return err
		}
	}
	if expected.RouterDraftGeneration != nil {
		if err := comparePublicationVersion(ctx, tx, "SELECT coalesce(max(generation),0) FROM router_settings WHERE id = ?",
			expected.RouterID, *expected.RouterDraftGeneration); err != nil {
			return err
		}
	}
	if plan.AcceptInitialTemplate {
		return comparePublicationVersion(ctx, tx,
			"SELECT EXISTS(SELECT 1 FROM active_router_publications WHERE template_id = ?)",
			expected.TemplateID, 0)
	}
	return nil
}

func comparePublicationVersion(ctx context.Context, tx *sql.Tx, query, id string, expected int64) error {
	var actual int64
	if err := tx.QueryRowContext(ctx, query, id).Scan(&actual); err != nil {
		return operationError("compare publication version", ctx.Err())
	}
	if actual != expected {
		return publication.ErrConflict
	}
	return nil
}

func checkPublicationOrderBasis(ctx context.Context, tx *sql.Tx, plan publication.CommitPlan) error {
	if plan.OrderBasis == nil {
		return nil
	}
	query := readAcceptedTemplate
	if plan.AcceptInitialTemplate {
		query = readTemplateDraft
	}
	template, err := readPublicationTemplate(ctx, tx, query, plan.Expected.TemplateID)
	if err != nil {
		return err
	}
	if !basisMatchesTemplate(plan.OrderBasis, template) {
		return publication.ErrInvalidState
	}
	return nil
}

func writePublicationRow(ctx context.Context, tx *sql.Tx, statement string, args ...any) error {
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return operationError("write publication", ctx.Err())
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return operationError("write publication", ctx.Err())
	}
	return nil
}
