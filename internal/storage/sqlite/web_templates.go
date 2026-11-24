package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
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
	return listTemplateSummaries(ctx, tx)
}

func listTemplateSummaries(ctx context.Context, tx *sql.Tx) ([]templates.Summary, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id, version FROM templates ORDER BY id COLLATE BINARY")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]templates.Summary, 0)
	for rows.Next() {
		var row templates.Summary
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

// Create inserts the prepared draft and renews valid browser activity in the same writer.
func (w WebTemplates) Create(ctx context.Context, digest [32]byte, clock func() time.Time, draft configuration.TemplateDraft) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := clock()
	if err := touchActiveWebSession(ctx, tx, now, activeWebSessionArgs(digest, now)); err != nil {
		return err
	}
	if err := insertTemplate(ctx, tx, draft); err != nil {
		return err
	}
	return tx.Commit()
}

func (w WebTemplates) Copy(ctx context.Context, digest [32]byte, clock func() time.Time, source, target string, expected int64) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := clock()
	if err := touchActiveWebSession(ctx, tx, now, activeWebSessionArgs(digest, now)); err != nil {
		return err
	}
	var raw []byte
	var version int64
	err = tx.QueryRowContext(ctx, "SELECT draft, version FROM templates WHERE id = ?", source).Scan(&raw, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return templates.ErrNotFound
	}
	if err != nil {
		return operationError("read template to copy", ctx.Err())
	}
	if version != expected {
		return templates.ErrVersionConflict
	}
	draft, err := decodeTemplate(source, raw)
	if err != nil || len(configuration.ValidateTemplateDraft(*draft)) != 0 {
		return templates.ErrCorrupt
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO templates (id,draft) VALUES (?,?) ON CONFLICT(id) DO NOTHING", target, raw)
	if err != nil {
		return operationError("insert template copy", ctx.Err())
	}
	n, err := result.RowsAffected()
	if err != nil {
		return operationError("confirm template copy", ctx.Err())
	}
	if n != 1 {
		return templates.ErrConflict
	}
	// Template element IDs are scoped by the template, including the separate XKeen source.
	_, err = tx.ExecContext(ctx, `INSERT INTO shared_xkeen_exclusions(template_id,element_id,version,source,variables)
	 SELECT ?,element_id,1,source,variables FROM shared_xkeen_exclusions WHERE template_id=?`, target, source)
	if err != nil {
		return operationError("copy template exclusion source", ctx.Err())
	}
	return tx.Commit()
}
