package sqlite

import (
	"context"
	"database/sql"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func (s *ExternalAccess) CreateExternalTemplate(ctx context.Context, permit externalaccess.Permit, id string) (templates.Summary, error) {
	draft, err := templates.PrepareDraft(configuration.TemplateDraft{ID: id})
	if err != nil {
		return templates.Summary{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return templates.Summary{}, err
	}
	defer tx.Rollback()
	if err := requireExternalPermit(ctx, tx, permit); err != nil {
		return templates.Summary{}, err
	}
	if err := insertTemplate(ctx, tx, draft); err != nil {
		return templates.Summary{}, err
	}
	if err := tx.Commit(); err != nil {
		return templates.Summary{}, err
	}
	return templates.Summary{ID: draft.ID, Version: 1}, nil
}

func (s *ExternalAccess) ReadExternalTemplate(ctx context.Context, permit externalaccess.Permit, id string) (*templates.Snapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := requireExternalPermit(ctx, tx, permit); err != nil {
		return nil, err
	}
	snap, err := findTemplate(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return templates.RestoreSnapshot(id, snap)
}

func (s *ExternalAccess) ReplaceExternalTemplate(ctx context.Context, permit externalaccess.Permit, draft configuration.TemplateDraft, expected int64) (int64, error) {
	raw, err := encodeRestoredTemplate(draft)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := requireExternalPermit(ctx, tx, permit); err != nil {
		return 0, err
	}
	version, err := replaceTemplate(ctx, tx, draft.ID, raw, expected)
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return version, nil
}

func (s *ExternalAccess) ListExternalTemplates(ctx context.Context, permit externalaccess.Permit) ([]templates.Summary, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := requireExternalPermit(ctx, tx, permit); err != nil {
		return nil, err
	}
	return listTemplateSummaries(ctx, tx)
}
