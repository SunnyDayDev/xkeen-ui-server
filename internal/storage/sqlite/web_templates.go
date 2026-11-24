package sqlite

import (
	"context"
	"database/sql"
	"time"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type WebTemplates struct{ db *sql.DB }

func NewWebTemplates(db *sql.DB) WebTemplates { return WebTemplates{db: db} }

func (w WebTemplates) List(ctx context.Context, digest [32]byte, clock func() time.Time) ([]webadmin.TemplateSummary, error) {
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := requireActiveWebSession(ctx, tx, activeWebSessionArgs(digest, clock())); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id, version FROM templates ORDER BY id COLLATE BINARY")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]webadmin.TemplateSummary, 0)
	for rows.Next() {
		var row webadmin.TemplateSummary
		if err := rows.Scan(&row.ID, &row.Version); err != nil {
			return nil, err
		}
		if row.ID == "" || !utf8.ValidString(row.ID) || row.Version < 1 {
			return nil, templates.ErrCorrupt
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
