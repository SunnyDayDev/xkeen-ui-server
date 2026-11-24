package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func (w WebTemplates) ReadTemplate(ctx context.Context, digest [32]byte, clock func() time.Time, id string) (*templates.Snapshot, error) {
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := requireActiveWebSession(ctx, tx, activeWebSessionArgs(digest, clock())); err != nil {
		return nil, err
	}
	snapshot, err := findTemplate(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return templates.RestoreSnapshot(id, snapshot)
}

func (w WebTemplates) ReplaceTemplate(ctx context.Context, digest [32]byte, clock func() time.Time, draft configuration.TemplateDraft, expectedVersion int64) (int64, error) {
	if expectedVersion < 1 {
		return 0, templates.ErrInvalidVersion
	}
	raw, err := encodeRestoredTemplate(draft)
	if err != nil {
		return 0, err
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
	version, err := replaceTemplate(ctx, tx, draft.ID, raw, expectedVersion)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return version, nil
}
