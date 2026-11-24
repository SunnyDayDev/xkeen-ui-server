package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

type WebRouterName struct{ db *sql.DB }

func NewWebRouterName(db *sql.DB) WebRouterName { return WebRouterName{db: db} }

func (s WebRouterName) Read(ctx context.Context, digest [32]byte, clock func() time.Time, id string) (routersettings.DisplayNameSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	defer tx.Rollback()
	if err := requireActiveWebSession(ctx, tx, activeWebSessionArgs(digest, clock())); err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	return readVisibleRouterName(ctx, tx, id)
}

func (s WebRouterName) Save(ctx context.Context, digest [32]byte, clock func() time.Time, expected routersettings.DisplayNameSnapshot, name string) (routersettings.DisplayNameSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	defer tx.Rollback()
	now := clock()
	if err := touchActiveWebSession(ctx, tx, now, activeWebSessionArgs(digest, now)); err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	current, err := readVisibleRouterName(ctx, tx, expected.RouterID)
	if err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	if current.Version != expected.Version || current.ActiveGeneration != expected.ActiveGeneration {
		return routersettings.DisplayNameSnapshot{}, routersettings.ErrDisplayNameConflict
	}
	result, err := updateRouterName(ctx, tx, current, name)
	if err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	return result, nil
}

func readVisibleRouterName(ctx context.Context, tx *sql.Tx, id string) (routersettings.DisplayNameSnapshot, error) {
	var visible bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1`+visibleRouterSource+` AND active.router_id=?)`, id).Scan(&visible); err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	if !visible {
		return routersettings.DisplayNameSnapshot{}, routersettings.ErrVisibilityNotFound
	}
	return readRouterName(ctx, tx, id)
}
