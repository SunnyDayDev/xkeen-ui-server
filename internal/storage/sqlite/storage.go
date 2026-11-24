// Package sqlite owns the server's SQLite file and schema migrations.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const createMigrationHistory = "CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)"

const createRouterSettings = `CREATE TABLE router_settings (
	id TEXT NOT NULL PRIMARY KEY COLLATE BINARY,
	snapshot BLOB NOT NULL
)`

const createPublicationStorage = `
	ALTER TABLE router_settings ADD COLUMN generation INTEGER NOT NULL DEFAULT 1
		CHECK (typeof(generation) = 'integer' AND generation >= 1);
	CREATE TABLE accepted_templates (
		template_id TEXT NOT NULL COLLATE BINARY,
		version INTEGER NOT NULL CHECK (typeof(version) = 'integer' AND version >= 1),
		draft BLOB NOT NULL,
		PRIMARY KEY (template_id, version)
	);
	CREATE TABLE active_router_publications (
		router_id TEXT NOT NULL PRIMARY KEY COLLATE BINARY,
		template_id TEXT NOT NULL COLLATE BINARY,
		generation INTEGER NOT NULL CHECK (typeof(generation) = 'integer' AND generation >= 1),
		settings BLOB NOT NULL,
		order_basis BLOB
	);
	CREATE INDEX active_router_publications_template_id ON active_router_publications(template_id);
	CREATE TABLE desired_router_revisions (
		router_id TEXT NOT NULL COLLATE BINARY,
		revision INTEGER NOT NULL CHECK (typeof(revision) = 'integer' AND revision >= 1),
		profile TEXT NOT NULL,
		files BLOB NOT NULL,
		PRIMARY KEY (router_id, revision)
	)`

const createAgentCredentialStorage = `
	CREATE TABLE agent_registrations (
		router_id TEXT NOT NULL PRIMARY KEY COLLATE BINARY,
		generation INTEGER NOT NULL CHECK (typeof(generation) = 'integer' AND generation >= 0),
		active INTEGER NOT NULL CHECK (active IN (0,1))
	);
	CREATE TABLE agent_working_keys (
		selector BLOB NOT NULL PRIMARY KEY CHECK (typeof(selector) = 'blob' AND length(selector) = 16),
		router_id TEXT NOT NULL COLLATE BINARY,
		generation INTEGER NOT NULL CHECK (typeof(generation) = 'integer' AND generation > 0),
		secret_digest BLOB NOT NULL UNIQUE CHECK (typeof(secret_digest) = 'blob' AND length(secret_digest) = 32),
		UNIQUE (router_id, generation)
	);
	CREATE TABLE agent_invitations (
		selector BLOB NOT NULL PRIMARY KEY CHECK (typeof(selector) = 'blob' AND length(selector) = 16),
		router_id TEXT NOT NULL COLLATE BINARY,
		purpose INTEGER NOT NULL CHECK (purpose IN (1,2)),
		expires_ns INTEGER NOT NULL CHECK (typeof(expires_ns) = 'integer'),
		secret_digest BLOB NOT NULL CHECK (typeof(secret_digest) = 'blob' AND length(secret_digest) = 32),
		expected_generation INTEGER NOT NULL CHECK (typeof(expected_generation) = 'integer' AND expected_generation >= 0),
		redeemed_generation INTEGER CHECK (redeemed_generation IS NULL OR
			(typeof(redeemed_generation) = 'integer' AND redeemed_generation > 0))
	);
	CREATE UNIQUE INDEX agent_one_pending_invitation ON agent_invitations(router_id) WHERE redeemed_generation IS NULL`

// Migrations are indexed by version minus one and remain immutable once released.
var migrations = []string{
	createMigrationHistory,
	"CREATE TABLE templates (id TEXT PRIMARY KEY NOT NULL, draft BLOB NOT NULL)",
	createRouterSettings,
	`ALTER TABLE templates ADD COLUMN version INTEGER NOT NULL DEFAULT 1
		CHECK (typeof(version) = 'integer' AND version >= 1)`,
	createPublicationStorage,
	createAgentCredentialStorage,
	createContactStorage,
	createWebAdminStorage,
	createExternalAccess,
	createRouterVisibility,
	`CREATE TABLE agent_invitation_epochs (
		router_id TEXT PRIMARY KEY NOT NULL COLLATE BINARY,
		epoch INTEGER NOT NULL CHECK (typeof(epoch) = 'integer' AND epoch > 0)
	)`,
	createSharedExclusions,
	`ALTER TABLE router_settings ADD COLUMN order_basis BLOB`,
	// Older readers reject personal exclusion records in the existing snapshot columns.
	`SELECT 1`,
}

func Open(ctx context.Context, path string) (*sql.DB, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0600)
	switch {
	case err == nil:
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("open SQLite: %w", err)
		}
	case errors.Is(err, os.ErrExist):
	default:
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	return OpenExisting(ctx, path)
}

// OpenExisting requires an existing regular database and applies migrations.
func OpenExisting(ctx context.Context, path string) (*sql.DB, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("open SQLite: database path must be a regular file")
	}
	// Preserve parent traversal through symlinks, as when creating the file.
	if !filepath.IsAbs(path) {
		workingDir, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("open SQLite: %w", err)
		}
		path = workingDir + string(os.PathSeparator) + path
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	params := url.Values{
		"mode":    {"rw"},
		"_pragma": {"busy_timeout(1000)"},
		"_txlock": {"immediate"},
	}
	uri.RawQuery = params.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := checkDatabase(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	if err := migrate(ctx, db, migrations); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate SQLite: %w", err)
	}
	return db, nil
}

func checkDatabase(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var objectCount int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema").Scan(&objectCount); err != nil {
		return err
	}
	// SQLite can fall back to read-only when the operating system denies writes.
	return conn.Raw(func(driverConn any) error {
		sqliteConn, ok := driverConn.(interface{ IsReadOnly(string) (bool, error) })
		if !ok {
			return errors.New("SQLite driver cannot check write access")
		}
		readOnly, err := sqliteConn.IsReadOnly("main")
		if err != nil {
			return err
		}
		if readOnly {
			return errors.New("database is read-only")
		}
		return nil
	})
}

func migrate(ctx context.Context, db *sql.DB, migrations []string) error {
	for {
		current, err := applyNextMigration(ctx, db, migrations)
		if err != nil || current {
			return err
		}
	}
}

// applyNextMigration checks and advances the version under the same write lock.
func applyNextMigration(ctx context.Context, db *sql.DB, migrations []string) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	version, err := readVersion(ctx, tx)
	if err != nil {
		return false, err
	}
	if version > len(migrations) {
		return false, fmt.Errorf("schema version %d exceeds supported version %d", version, len(migrations))
	}
	if version == len(migrations) {
		return true, nil
	}

	next := version + 1
	if _, err := tx.ExecContext(ctx, migrations[version]); err != nil {
		return false, fmt.Errorf("version %d: %w", next, err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations VALUES (?)", next); err != nil {
		return false, fmt.Errorf("record version %d: %w", next, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit version %d: %w", next, err)
	}
	return next == len(migrations), nil
}

func readVersion(ctx context.Context, tx *sql.Tx) (int, error) {
	var definition sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_schema WHERE name = 'schema_migrations'").Scan(&definition)
	if errors.Is(err, sql.ErrNoRows) {
		var objects int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema").Scan(&objects); err != nil {
			return 0, err
		}
		if objects != 0 {
			return 0, errors.New("database has objects without migration history")
		}
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if definition.String != createMigrationHistory {
		return 0, errors.New("invalid migration history table")
	}
	var hasTrigger bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type = 'trigger' AND tbl_name = 'schema_migrations')").Scan(&hasTrigger); err != nil {
		return 0, err
	}
	if hasTrigger {
		return 0, errors.New("migration history cannot have triggers")
	}
	var count, first, last int
	if err := tx.QueryRowContext(ctx, "SELECT count(*), coalesce(min(version), 0), coalesce(max(version), 0) FROM schema_migrations").Scan(&count, &first, &last); err != nil {
		return 0, err
	}
	if first != 1 || last != count {
		return 0, errors.New("migration history must contain consecutive versions starting at 1")
	}
	return last, nil
}
