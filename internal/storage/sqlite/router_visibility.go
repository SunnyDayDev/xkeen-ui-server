package sqlite

import (
	"context"
	"database/sql"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

const createRouterVisibility = `CREATE TABLE hidden_router_visibility (
	router_id TEXT NOT NULL PRIMARY KEY COLLATE BINARY
		REFERENCES active_router_publications(router_id)
)`

type RouterVisibility struct{ db *sql.DB }

func NewRouterVisibility(db *sql.DB) RouterVisibility {
	return RouterVisibility{db: db}
}

func (v RouterVisibility) Hide(ctx context.Context, id string) error {
	tx, err := v.db.BeginTx(ctx, nil)
	if err != nil {
		return routersettings.ErrVisibilityUnavailable
	}
	defer tx.Rollback()

	var confirmed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM active_router_publications WHERE router_id=?
	)`, id).Scan(&confirmed); err != nil {
		return routersettings.ErrVisibilityUnavailable
	}
	if !confirmed {
		return routersettings.ErrVisibilityNotFound
	}
	if err := hideRouter(ctx, tx, id); err != nil {
		return routersettings.ErrVisibilityUnavailable
	}
	if err := tx.Commit(); err != nil {
		return routersettings.ErrVisibilityUnavailable
	}
	return nil
}

const createRouterVisibilityEpoch = `CREATE TABLE router_visibility_epochs (
	router_id TEXT NOT NULL PRIMARY KEY COLLATE BINARY REFERENCES active_router_publications(router_id),
	epoch INTEGER NOT NULL CHECK(typeof(epoch) = 'integer' AND epoch > 0)
);
INSERT INTO router_visibility_epochs (router_id,epoch)
	SELECT router_id,1 FROM hidden_router_visibility;`

func hideRouter(ctx context.Context, tx *sql.Tx, id string) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO hidden_router_visibility(router_id)
		VALUES (?) ON CONFLICT(router_id) DO NOTHING`, id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 0 {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO router_visibility_epochs(router_id,epoch)
		VALUES (?,1) ON CONFLICT(router_id) DO UPDATE SET epoch=epoch+1`, id)
	return err
}

func (v RouterVisibility) VisibleIDs(ctx context.Context) ([]string, error) {
	rows, err := v.db.QueryContext(ctx, `SELECT active.router_id`+visibleRouterSource+
		` ORDER BY active.router_id COLLATE BINARY`)
	if err != nil {
		return nil, routersettings.ErrVisibilityUnavailable
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil || id == "" || !utf8.ValidString(id) {
			return nil, routersettings.ErrVisibilityUnavailable
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, routersettings.ErrVisibilityUnavailable
	}
	return ids, nil
}

const visibleRouterSource = `
	FROM active_router_publications AS active
	JOIN agent_contacts AS contact ON contact.router_id=active.router_id
	WHERE NOT EXISTS (SELECT 1 FROM hidden_router_visibility AS hidden WHERE hidden.router_id=active.router_id)`
