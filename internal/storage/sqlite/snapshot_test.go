package sqlite

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotValidatesHistoryAndMigratesOnlyRestoredCopy(t *testing.T) {
	dir := t.TempDir()
	ctx := t.Context()
	oldVersion := len(migrations) - 1
	seed := func(name string) (*sql.DB, string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := migrate(ctx, db, migrations[:oldVersion]); err != nil {
			t.Fatal(err)
		}
		return db, path
	}
	read := func(path string) []byte {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	version := func(path string) int {
		t.Helper()
		db, err := openReadOnlySnapshot(path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		n, err := inspectSnapshot(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	db, old := seed("old.sqlite")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "snapshot.sqlite")
	if err := Backup(ctx, old, backup); err != nil {
		t.Fatal(err)
	}
	original := read(backup)
	out := filepath.Join(dir, "restored.sqlite")
	if err := Restore(ctx, backup, out); err != nil {
		t.Fatal(err)
	}
	if version(old) != oldVersion || version(backup) != oldVersion || version(out) != len(migrations) || !bytes.Equal(original, read(backup)) {
		t.Fatal("only the restored copy should have migrated")
	}
	for name, query := range map[string]string{
		"mimic": "CREATE TABLE foreign_table (id INTEGER)",
		"newer": "INSERT INTO schema_migrations VALUES ((SELECT max(version)+1 FROM schema_migrations)), ((SELECT max(version)+2 FROM schema_migrations))",
	} {
		t.Run(name, func(t *testing.T) {
			db, in := seed(name + ".sqlite")
			if _, err := db.ExecContext(ctx, query); err != nil {
				t.Fatal(err)
			}
			rejected := filepath.Join(dir, name+"-rejected.sqlite")
			if err := Restore(ctx, in, rejected); !errors.Is(err, errSnapshotInvalid) {
				t.Fatalf("restore returned %v", err)
			}
			if _, err := os.Lstat(rejected); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid snapshot published an output")
			}
		})
	}
}

func TestSnapshotRefusesLinksAndConflictingSidecars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.sqlite")
	db, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	out := filepath.Join(dir, "backup.sqlite")
	link := filepath.Join(dir, "link.sqlite")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := Backup(t.Context(), link, out); !errors.Is(err, errSnapshotPath) {
		t.Fatalf("linked input accepted: %v", err)
	}
	if err := os.WriteFile(out+"-wal", []byte("unrelated sidecar"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Backup(t.Context(), path, out); !errors.Is(err, errSnapshotExists) {
		t.Fatalf("conflicting destination accepted: %v", err)
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("conflicting destination was published")
	}
}
