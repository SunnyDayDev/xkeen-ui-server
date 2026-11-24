package sqlite

import (
	"context"
	"database/sql"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

func (s *ExternalAccess) ReadExternalRouterApplication(ctx context.Context, permit externalaccess.Permit, id string, revision int64) (reporting.Status, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return reporting.Status{}, err
	}
	defer tx.Rollback()
	if err := requireExternalPermit(ctx, tx, permit); err != nil {
		return reporting.Status{}, err
	}
	var known bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM desired_router_revisions AS revisions
		JOIN active_router_publications AS active ON active.router_id=revisions.router_id
		WHERE revisions.router_id=? AND revisions.revision=?
	)`, id, revision).Scan(&known)
	if err != nil {
		return reporting.Status{}, err
	}
	if !known {
		return reporting.Status{}, reporting.ErrUnknownRevision
	}
	return readContactStatus(ctx, tx, id, s.now())
}
