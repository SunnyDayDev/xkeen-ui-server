package sqlite

import (
	"context"
	"database/sql"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

func (s *ExternalAccess) ReadExternalRouterDraft(ctx context.Context, permit externalaccess.Permit, id string) (routerdraft.Snapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return routerdraft.Snapshot{}, err
	}
	defer tx.Rollback()
	if err := requireExternalPermit(ctx, tx, permit); err != nil {
		return routerdraft.Snapshot{}, err
	}
	return readRouterDraft(ctx, tx, id)
}

func (s *ExternalAccess) WriteExternalRouterDraft(ctx context.Context, permit externalaccess.Permit, basis routerdraft.Basis, draft routersettings.Draft) (int64, error) {
	return writeRouterDraft(ctx, s.db, basis, draft, nil, func(ctx context.Context, tx *sql.Tx) error {
		return requireExternalPermit(ctx, tx, permit)
	})
}
