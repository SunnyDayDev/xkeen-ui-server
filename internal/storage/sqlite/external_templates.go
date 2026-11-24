package sqlite

import (
	"context"
	"database/sql"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
)

func requireExternalPermit(ctx context.Context, tx *sql.Tx, permit externalaccess.Permit) error {
	state, _, err := readExternalSettings(ctx, tx)
	if err != nil {
		return err
	}
	if !state.Enabled {
		return externalaccess.ErrDisabled
	}
	if permit.Version <= 0 || state.Version != permit.Version {
		return externalaccess.ErrUnauthorized
	}
	return nil
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
