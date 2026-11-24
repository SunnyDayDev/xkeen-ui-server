package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestRouterSettingsMigrationPreservesEarlierData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "earlier.sqlite")
	earlier, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer earlier.Close()
	// Versions 1 and 2 are the accepted C01 and C02 schema.
	if err := migrate(ctx, earlier, migrations[:2]); err != nil {
		t.Fatal(err)
	}
	execSQL(t, earlier, "INSERT INTO templates (id, draft) VALUES ('fixture.example', x'5B5D')")
	if err := earlier.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := readText(t, db, "SELECT CAST(draft AS TEXT) FROM templates WHERE id = 'fixture.example'"); got != "[]" {
		t.Fatalf("migration changed existing data: %q", got)
	}
	if got := readText(t, db, "SELECT version FROM schema_migrations WHERE version = 3"); got != "3" {
		t.Fatalf("migration version = %q", got)
	}
	if got := readText(t, db, "SELECT name FROM sqlite_schema WHERE name = 'router_settings'"); got != "router_settings" {
		t.Fatalf("router settings table = %q", got)
	}
}
