package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

type WebRouterStatus struct{ db *sql.DB }

func NewWebRouterStatus(db *sql.DB) WebRouterStatus {
	return WebRouterStatus{db: db}
}

func (s WebRouterStatus) List(ctx context.Context, digest [32]byte, clock func() time.Time) ([]webadmin.RouterStatus, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := clock()
	if err := requireActiveWebSession(ctx, tx, activeWebSessionArgs(digest, now)); err != nil {
		return nil, err
	}
	ids, err := visibleRouterIDs(ctx, tx)
	if err != nil {
		return nil, err
	}
	rows := make([]webadmin.RouterStatus, 0, len(ids))
	for _, id := range ids {
		active, err := readActivePublication(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		status, err := readContactStatus(ctx, tx, id, now)
		if err != nil {
			return nil, err
		}
		if active == nil || status.Presence == reporting.Unobserved || status.Desired < 1 {
			return nil, publication.ErrStoredState
		}
		name := active.Draft.Name
		if name == "" {
			name = id
		}
		rows = append(rows, webadmin.RouterStatus{RouterID: id, Name: name, TemplateID: active.Draft.TemplateID,
			Contact: status.Presence, Desired: status.Desired, Applied: status.Applied, Runtime: status.Runtime})
	}
	return rows, nil
}
