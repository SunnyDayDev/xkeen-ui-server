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
	execSQL(t, earlier, createMigrationHistory)
	execSQL(t, earlier, "INSERT INTO schema_migrations VALUES (1)")
	execSQL(t, earlier, "CREATE TABLE earlier_data (value TEXT NOT NULL)")
	execSQL(t, earlier, "INSERT INTO earlier_data VALUES ('fixture.example')")
	if err := earlier.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := readText(t, db, "SELECT value FROM earlier_data"); got != "fixture.example" {
		t.Fatalf("migration changed existing data: %q", got)
	}
	if got := readText(t, db, "SELECT name FROM sqlite_schema WHERE name = 'router_settings'"); got != "router_settings" {
		t.Fatalf("router settings table = %q", got)
	}
}
