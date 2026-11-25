package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

type WebRouterVisibility struct{ db *sql.DB }

func NewWebRouterVisibility(db *sql.DB) WebRouterVisibility {
	return WebRouterVisibility{db: db}
}

func (s WebRouterVisibility) Read(ctx context.Context, digest [32]byte, clock func() time.Time, id string) (routersettings.VisibilitySnapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return routersettings.VisibilitySnapshot{}, err
	}
	defer tx.Rollback()
	if err := requireActiveWebSession(ctx, tx, activeWebSessionArgs(digest, clock())); err != nil {
		return routersettings.VisibilitySnapshot{}, err
	}
	return readRouterVisibility(ctx, tx, id)
}

func (s WebRouterVisibility) Hide(ctx context.Context, digest [32]byte, clock func() time.Time, expected routersettings.VisibilitySnapshot) (routersettings.VisibilitySnapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return routersettings.VisibilitySnapshot{}, err
	}
	defer tx.Rollback()
	now := clock()
	if err := touchActiveWebSession(ctx, tx, now, activeWebSessionArgs(digest, now)); err != nil {
		return routersettings.VisibilitySnapshot{}, err
	}
	result, err := hideMatchingRouter(ctx, tx, expected)
	if err != nil {
		return routersettings.VisibilitySnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return routersettings.VisibilitySnapshot{}, err
	}
	return result, nil
}
