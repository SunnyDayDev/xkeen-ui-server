package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"syscall"
)

var (
	errSnapshotPath     = errors.New("storage snapshot requires protected owner files and directories")
	errSnapshotExists   = errors.New("storage snapshot destination already exists")
	errSnapshotInvalid  = errors.New("storage snapshot has an unsupported or damaged database")
	errSnapshotIO       = errors.New("storage snapshot could not be completed")
	errSnapshotUnsynced = errors.New("storage snapshot is visible but its durability is unconfirmed")
)

// Backup preserves the source's supported schema and committed contents.
func Backup(ctx context.Context, source, output string) error {
	return snapshot(ctx, source, output, false)
}

// Restore prepares and migrates a new file without activating it.
func Restore(ctx context.Context, source, output string) error {
	return snapshot(ctx, source, output, true)
}

func snapshot(ctx context.Context, source, output string, restore bool) error {
	in, err := snapshotPath(source)
	if err != nil {
		return err
	}
	out, err := snapshotPath(output)
	if err != nil {
		return err
	}
	info, err := os.Lstat(in)
	if err != nil || !snapshotFile(info) {
		return errSnapshotPath
	}
	if err := vacantSnapshot(out); err != nil {
		return err
	}

	stage, err := os.MkdirTemp(filepath.Dir(out), ".xkeen-snapshot-")
	if err != nil {
		return errSnapshotIO
	}
	defer os.RemoveAll(stage)
	copyPath := filepath.Join(stage, "database.sqlite")
	file, err := os.OpenFile(copyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errSnapshotIO
	}
	if err := file.Close(); err != nil {
		return errSnapshotIO
	}

	sourceDB, err := openReadOnlySnapshot(in)
	if err != nil {
		return errSnapshotInvalid
	}
	defer sourceDB.Close()
	if _, err := inspectSnapshot(ctx, sourceDB); err != nil {
		return errSnapshotInvalid
	}
	if _, err := sourceDB.ExecContext(ctx, "VACUUM INTO ?", copyPath); err != nil {
		return errSnapshotIO
	}
	if err := sourceDB.Close(); err != nil {
		return errSnapshotIO
	}

	if err := verifySnapshotFile(ctx, copyPath, false); err != nil {
		return err
	}
	if restore {
		migrated, err := OpenExisting(ctx, copyPath)
		if err != nil {
			return errSnapshotInvalid
		}
		if err := migrated.Close(); err != nil {
			return errSnapshotIO
		}
		if err := verifySnapshotFile(ctx, copyPath, true); err != nil {
			return err
		}
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if _, err := os.Lstat(copyPath + suffix); !errors.Is(err, os.ErrNotExist) {
			return errSnapshotIO
		}
	}
	if ctx.Err() != nil {
		return errSnapshotIO
	}
	return publishSnapshot(copyPath, out)
}

func snapshotPath(path string) (string, error) {
	if path == "" {
		return "", errSnapshotPath
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", errSnapshotPath
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return "", errSnapshotPath
	}
	for current := parent; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() {
			return "", errSnapshotPath
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return "", errSnapshotPath
		}
		owner := uint32(os.Geteuid())
		if current == parent {
			if stat.Uid != owner || info.Mode().Perm()&0022 != 0 {
				return "", errSnapshotPath
			}
		} else if (stat.Uid != 0 && stat.Uid != owner) || (info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0) {
			return "", errSnapshotPath
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

func snapshotFile(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0 && stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1
}

func vacantSnapshot(path string) error {
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		_, err := os.Lstat(path + suffix)
		if err == nil {
			return errSnapshotExists
		}
		if !errors.Is(err, os.ErrNotExist) {
			return errSnapshotPath
		}
	}
	return nil
}

func openReadOnlySnapshot(path string) (*sql.DB, error) {
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: url.Values{
		"mode": {"ro"}, "_pragma": {"trusted_schema(0)", "busy_timeout(1000)"},
	}.Encode()}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func verifySnapshotFile(ctx context.Context, path string, current bool) error {
	db, err := openReadOnlySnapshot(path)
	if err != nil {
		return errSnapshotInvalid
	}
	defer db.Close()
	version, err := inspectSnapshot(ctx, db)
	if err != nil || (current && version != len(migrations)) {
		return errSnapshotInvalid
	}
	return nil
}

func inspectSnapshot(ctx context.Context, db *sql.DB) (int, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	version, err := readVersion(ctx, tx)
	if err != nil || version < 1 || version > len(migrations) {
		return 0, errSnapshotInvalid
	}
	wanted, err := referenceSnapshotSchema(ctx, version)
	if err != nil {
		return 0, err
	}
	observed, err := snapshotSchema(ctx, tx)
	if err != nil || !slices.Equal(observed, wanted) {
		return 0, errSnapshotInvalid
	}
	if err := checkSnapshotIntegrity(ctx, tx); err != nil {
		return 0, err
	}
	return version, nil
}

type snapshotObject struct {
	kind, name, table string
	sql               sql.NullString
}

func referenceSnapshotSchema(ctx context.Context, version int) ([]snapshotObject, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if err := migrate(ctx, db, migrations[:version]); err != nil {
		return nil, err
	}
	return snapshotSchema(ctx, db)
}

func snapshotSchema(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) ([]snapshotObject, error) {
	rows, err := query.QueryContext(ctx, `SELECT type,name,tbl_name,sql FROM sqlite_schema
  WHERE name NOT GLOB 'sqlite_*' ORDER BY type,name COLLATE BINARY`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []snapshotObject
	for rows.Next() {
		var object snapshotObject
		if err := rows.Scan(&object.kind, &object.name, &object.table, &object.sql); err != nil {
			return nil, err
		}
		result = append(result, object)
	}
	return result, rows.Err()
}

func checkSnapshotIntegrity(ctx context.Context, tx *sql.Tx) error {
	var text string
	if err := tx.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&text); err != nil || text != "ok" {
		return errSnapshotInvalid
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return errSnapshotInvalid
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		return errSnapshotInvalid
	}
	return nil
}

func publishSnapshot(stage, output string) error {
	file, err := os.Open(stage)
	if err != nil {
		return errSnapshotIO
	}
	info, statErr := file.Stat()
	if statErr != nil || !snapshotFile(info) {
		file.Close()
		return errSnapshotPath
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		return errSnapshotIO
	}
	if err := vacantSnapshot(output); err != nil {
		return err
	}
	if err := os.Link(stage, output); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errSnapshotExists
		}
		return errSnapshotIO
	}
	if err := os.Remove(stage); err != nil {
		return errSnapshotUnsynced
	}
	for _, directory := range []string{filepath.Dir(stage), filepath.Dir(output)} {
		parent, err := os.Open(directory)
		if err != nil {
			return errSnapshotUnsynced
		}
		if err := errors.Join(parent.Sync(), parent.Close()); err != nil {
			return errSnapshotUnsynced
		}
	}
	return nil
}
