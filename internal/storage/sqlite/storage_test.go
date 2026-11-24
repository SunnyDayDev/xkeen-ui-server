package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestOpenAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "data #1?mode=memory.sqlite")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "CREATE TABLE sample (value TEXT NOT NULL)")
	started := time.Now()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO sample VALUES (?)", "demo.example"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Logf("commit one synthetic record: %s", time.Since(started))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("new database permissions = %o", info.Mode().Perm())
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := readText(t, db, "SELECT value FROM sample"); got != "demo.example" {
		t.Fatalf("stored value = %q", got)
	}
	if got := readText(t, db, "SELECT group_concat(version) FROM schema_migrations"); got != "1" {
		t.Fatalf("migration history = %q", got)
	}
	t.Logf("SQLite %s", readText(t, db, "SELECT sqlite_version()"))
}

func TestOpenFailures(t *testing.T) {
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "corrupt.sqlite")
	if err := os.WriteFile(corrupt, []byte(strings.Repeat("not sqlite", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, corrupt, filepath.Join(dir, "missing", "server.sqlite")} {
		db, err := Open(context.Background(), path)
		if db != nil || err == nil {
			t.Fatalf("Open(%q) = %v, %v", path, db, err)
		}
	}
}

func execSQL(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

func readText(t *testing.T, db *sql.DB, statement string) string {
	t.Helper()
	var value string
	if err := db.QueryRow(statement).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRejectIncompatibleHistory(t *testing.T) {
	for _, test := range []struct {
		name       string
		statements []string
	}{
		{"unmanaged objects", []string{"CREATE TABLE legacy (name TEXT)"}},
		{"newer version", []string{"CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)", "INSERT INTO schema_migrations VALUES (1), (2)"}},
		{"missing version", []string{"CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)", "INSERT INTO schema_migrations VALUES (2)"}},
		{"empty history", []string{"CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)"}},
		{"wrong table shape", []string{"CREATE TABLE schema_migrations (version INTEGER)", "INSERT INTO schema_migrations VALUES (1)"}},
		{"trigger on history", []string{createMigrationHistory, "INSERT INTO schema_migrations VALUES (1)", "CREATE TRIGGER ignore_versions BEFORE INSERT ON schema_migrations BEGIN SELECT RAISE(IGNORE); END"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test.sqlite")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, statement := range test.statements {
				execSQL(t, db, statement)
			}
			before := readText(t, db, "SELECT group_concat(sql) FROM sqlite_schema")
			opened, err := Open(context.Background(), path)
			if err == nil {
				opened.Close()
				t.Errorf("opened database with incompatible history")
			} else if !strings.Contains(err.Error(), "migrate SQLite") {
				t.Errorf("unexpected diagnostic: %v", err)
			}
			if got := readText(t, db, "SELECT group_concat(sql) FROM sqlite_schema"); got != before {
				t.Errorf("schema changed from %q to %q", before, got)
			}
		})
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "CREATE TABLE sample (value TEXT NOT NULL)")
	execSQL(t, db, "INSERT INTO sample VALUES ('demo.example')")
	started := time.Now()
	err = migrate(ctx, db, []string{
		createMigrationHistory,
		"CREATE TABLE pending (id INTEGER); UPDATE sample SET value = 'discarded.example'; INSERT INTO missing VALUES (1)",
	})
	t.Logf("failed migration with rollback: %s", time.Since(started))
	if err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("migration error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := readText(t, db, "SELECT value FROM sample"); got != "demo.example" {
		t.Errorf("stored value = %q", got)
	}
	if got := readText(t, db, "SELECT group_concat(version) FROM schema_migrations"); got != "1" {
		t.Errorf("migration history = %q", got)
	}
	if got := readText(t, db, "SELECT count(*) FROM sqlite_schema WHERE name = 'pending'"); got != "0" {
		t.Errorf("rolled-back table exists: %q", got)
	}
}

func TestConcurrentFirstOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite")
	start := make(chan struct{})
	done := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			db, err := Open(context.Background(), path)
			if err == nil {
				err = db.Close()
			}
			done <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := readText(t, db, "SELECT group_concat(version) FROM schema_migrations"); got != "1" {
		t.Errorf("migration history = %q", got)
	}
}

func TestVisibleWriteFailure(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	execSQL(t, db, "PRAGMA query_only=ON")
	err = migrate(ctx, db, []string{createMigrationHistory, "CREATE TABLE pending (id INTEGER)"})
	if err == nil || !strings.Contains(err.Error(), "readonly") {
		t.Fatalf("migration write failure = %v", err)
	}
	t.Logf("expected write error: %v", err)
	if got := readText(t, db, "SELECT group_concat(version) FROM schema_migrations"); got != "1" {
		t.Errorf("migration history = %q", got)
	}
}

func TestOpenPathThroughSymlinkParent(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "parent", "nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(nested, link); err != nil {
		t.Fatal(err)
	}
	db, err := Open(context.Background(), link+"/../data.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := os.Stat(filepath.Join(dir, "parent", "data.sqlite")); err != nil {
		t.Fatal(err)
	}
}

func TestReadOnlyFileIsRejected(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("filesystem write permissions do not restrict root")
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "read-only.sqlite")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if db != nil {
		db.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("opening a read-only database returned %v", err)
	}
}
