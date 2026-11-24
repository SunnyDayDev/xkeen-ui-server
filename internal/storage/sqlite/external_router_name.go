package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

const createRouterDisplayName = `CREATE TABLE router_display_names (
	router_id TEXT NOT NULL PRIMARY KEY COLLATE BINARY REFERENCES active_router_publications(router_id),
	name TEXT NOT NULL CHECK(typeof(name) = 'text' AND name <> ''),
	version INTEGER NOT NULL CHECK(typeof(version) = 'integer' AND version > 0)
)`

func displayedRouterName(ctx context.Context, tx *sql.Tx, id, fallback string) (string, error) {
	var name string
	err := tx.QueryRowContext(ctx, "SELECT name FROM router_display_names WHERE router_id=?", id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return fallback, nil
	}
	if err != nil || !routersettings.ValidDisplayName(name) {
		return "", publication.ErrStoredState
	}
	return name, nil
}

func (s *ExternalAccess) ReadExternalRouterName(ctx context.Context, permit externalaccess.Permit, id string) (routersettings.DisplayNameSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	defer tx.Rollback()
	if err := requireExternalPermit(ctx, tx, permit); err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	return readRouterName(ctx, tx, id)
}

func (s *ExternalAccess) WriteExternalRouterName(ctx context.Context, permit externalaccess.Permit, expected routersettings.DisplayNameSnapshot, name string) (routersettings.DisplayNameSnapshot, error) {
	if !routersettings.ValidDisplayName(name) {
		return routersettings.DisplayNameSnapshot{}, routersettings.ErrInvalidDraft
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	defer tx.Rollback()
	if err := requireExternalPermit(ctx, tx, permit); err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	current, err := readRouterName(ctx, tx, expected.RouterID)
	if err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	if current != expected {
		return routersettings.DisplayNameSnapshot{}, externalaccess.ErrConflict
	}
	if current.Name == name {
		return current, nil
	}
	if current.Version == math.MaxInt64 {
		return routersettings.DisplayNameSnapshot{}, externalaccess.ErrConflict
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO router_display_names(router_id,name,version)
		VALUES (?,?,1) ON CONFLICT(router_id) DO UPDATE SET name=excluded.name,version=version+1`, current.RouterID, name)
	if err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return routersettings.DisplayNameSnapshot{}, publication.ErrStoredState
	}
	if err := tx.Commit(); err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	current.Name = name
	current.Version++
	return current, nil
}

func readRouterName(ctx context.Context, tx *sql.Tx, id string) (routersettings.DisplayNameSnapshot, error) {
	state := routersettings.DisplayNameSnapshot{RouterID: id, Name: id}
	var override sql.NullString
	var legacy []byte
	err := tx.QueryRowContext(ctx, `SELECT active.generation, json_quote(json_extract(CAST(active.settings AS TEXT),'$.Name')),
		metadata.name, coalesce(metadata.version,0)
		FROM active_router_publications AS active
		LEFT JOIN router_display_names AS metadata ON metadata.router_id=active.router_id
		WHERE active.router_id=?`, id).Scan(&state.ActiveGeneration, &legacy, &override, &state.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return routersettings.DisplayNameSnapshot{}, routersettings.ErrVisibilityNotFound
	}
	if err != nil || state.ActiveGeneration < 1 || state.Version < 0 {
		return routersettings.DisplayNameSnapshot{}, publication.ErrStoredState
	}
	if override.Valid {
		if state.Version == 0 || !routersettings.ValidDisplayName(override.String) {
			return routersettings.DisplayNameSnapshot{}, publication.ErrStoredState
		}
		state.Name = override.String
		return state, nil
	}
	var initial *string
	if state.Version != 0 || json.Unmarshal(legacy, &initial) != nil {
		return routersettings.DisplayNameSnapshot{}, publication.ErrStoredState
	}
	if initial != nil && *initial != "" {
		state.Name = *initial
	}
	return state, nil
}
