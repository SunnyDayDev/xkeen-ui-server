package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

func (s *ExternalAccess) ReadExternalRouterVisibility(ctx context.Context, permit externalaccess.Permit, id string) (routersettings.VisibilitySnapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return routersettings.VisibilitySnapshot{}, err
	}
	defer tx.Rollback()
	if err := requireExternalPermit(ctx, tx, permit); err != nil {
		return routersettings.VisibilitySnapshot{}, err
	}
	return readRouterVisibility(ctx, tx, id)
}

func (s *ExternalAccess) HideExternalRouter(ctx context.Context, permit externalaccess.Permit, expected routersettings.VisibilitySnapshot) (routersettings.VisibilitySnapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return routersettings.VisibilitySnapshot{}, err
	}
	defer tx.Rollback()
	if err := requireExternalPermit(ctx, tx, permit); err != nil {
		return routersettings.VisibilitySnapshot{}, err
	}
	current, err := readRouterVisibility(ctx, tx, expected.RouterID)
	if err != nil {
		return routersettings.VisibilitySnapshot{}, err
	}
	if current != expected {
		return routersettings.VisibilitySnapshot{}, externalaccess.ErrConflict
	}
	if err := hideRouter(ctx, tx, expected.RouterID); err != nil {
		return routersettings.VisibilitySnapshot{}, err
	}
	current, err = readRouterVisibility(ctx, tx, expected.RouterID)
	if err != nil {
		return routersettings.VisibilitySnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return routersettings.VisibilitySnapshot{}, err
	}
	return current, nil
}

func readRouterVisibility(ctx context.Context, tx *sql.Tx, id string) (routersettings.VisibilitySnapshot, error) {
	status := routersettings.VisibilitySnapshot{RouterID: id}
	err := tx.QueryRowContext(ctx, `SELECT active.generation, coalesce(epochs.epoch,0),
		NOT EXISTS (SELECT 1 FROM hidden_router_visibility WHERE router_id=active.router_id)
		FROM active_router_publications AS active
		JOIN agent_contacts AS contact ON contact.router_id=active.router_id
		LEFT JOIN router_visibility_epochs AS epochs ON epochs.router_id=active.router_id
		WHERE active.router_id=?`, id).Scan(&status.ActiveGeneration, &status.Epoch, &status.Visible)
	if errors.Is(err, sql.ErrNoRows) {
		return routersettings.VisibilitySnapshot{}, routersettings.ErrVisibilityNotFound
	}
	if err != nil || status.ActiveGeneration < 1 || status.Epoch < 0 {
		return routersettings.VisibilitySnapshot{}, routersettings.ErrVisibilityUnavailable
	}
	return status, nil
}
