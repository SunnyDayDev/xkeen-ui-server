package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

const createWebAdminStorage = `
	CREATE TABLE web_administrator (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		generation INTEGER NOT NULL CHECK (typeof(generation) = 'integer' AND generation > 0),
		version INTEGER NOT NULL CHECK (version = 1),
		salt BLOB NOT NULL CHECK (typeof(salt) = 'blob' AND length(salt) = 16),
		digest BLOB NOT NULL CHECK (typeof(digest) = 'blob' AND length(digest) = 32)
	);
	CREATE TABLE web_sessions (
		digest BLOB NOT NULL PRIMARY KEY CHECK (typeof(digest) = 'blob' AND length(digest) = 32),
		generation INTEGER NOT NULL CHECK (typeof(generation) = 'integer' AND generation > 0),
		issued_ns INTEGER NOT NULL CHECK (typeof(issued_ns) = 'integer'),
		activity_ns INTEGER NOT NULL CHECK (typeof(activity_ns) = 'integer' AND activity_ns >= issued_ns)
	)`

type WebAdmin struct{ db *sql.DB }

func NewWebAdmin(db *sql.DB) *WebAdmin { return &WebAdmin{db: db} }

func (w *WebAdmin) SaveVerifier(ctx context.Context, v webadmin.Verifier, reset bool) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if reset {
		result, err := tx.ExecContext(ctx, `UPDATE web_administrator SET generation = generation + 1, version = ?, salt = ?, digest = ? WHERE id = 1`, v.Version, v.Salt[:], v.Digest[:])
		if err := requireWebRow(result, err, webadmin.ErrSetupRequired); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM web_sessions`); err != nil {
			return err
		}
	} else {
		result, err := tx.ExecContext(ctx, `INSERT INTO web_administrator VALUES (1, 1, ?, ?, ?) ON CONFLICT(id) DO NOTHING`, v.Version, v.Salt[:], v.Digest[:])
		if err := requireWebRow(result, err, webadmin.ErrAlreadySetup); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (w *WebAdmin) Verifier(ctx context.Context) (webadmin.Verifier, int64, error) {
	var v webadmin.Verifier
	var generation int64
	var salt, digest []byte
	err := w.db.QueryRowContext(ctx, `SELECT version, salt, digest, generation FROM web_administrator WHERE id = 1`).Scan(&v.Version, &salt, &digest, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return v, 0, webadmin.ErrSetupRequired
	}
	if err != nil {
		return v, 0, err
	}
	if len(salt) != len(v.Salt) || len(digest) != len(v.Digest) || v.Version != 1 || generation <= 0 {
		return v, 0, errors.New("invalid administrator verifier")
	}
	copy(v.Salt[:], salt)
	copy(v.Digest[:], digest)
	return v, generation, nil
}

func (w *WebAdmin) Issue(ctx context.Context, generation int64, digest, prior [32]byte, now time.Time) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO web_sessions(digest,generation,issued_ns,activity_ns)
		SELECT ?,generation,?,? FROM web_administrator WHERE id = 1 AND generation = ?`, digest[:], now.UnixNano(), now.UnixNano(), generation)
	if err := requireWebRow(result, err, webadmin.ErrSignedOut); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM web_sessions WHERE digest = ? OR issued_ns <= ? OR activity_ns <= ?`, prior[:], now.Add(-webadmin.AbsoluteLifetime).UnixNano(), now.Add(-webadmin.IdleLifetime).UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

// Sample the clock after acquiring the session transaction. An action
// waiting for the writer cannot renew a session that expired in the queue.
func (w *WebAdmin) Session(ctx context.Context, digest [32]byte, clock func() time.Time, action webadmin.SessionAction) error {
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: action == webadmin.SessionRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := clock()
	args := activeWebSessionArgs(digest, now)
	var result sql.Result
	switch action {
	case webadmin.SessionRead:
		return requireActiveWebSession(ctx, tx, args)
	case webadmin.SessionTouch:
		if err := touchActiveWebSession(ctx, tx, now, args); err != nil {
			return err
		}
		return tx.Commit()
	case webadmin.SessionRevoke:
		result, err = tx.ExecContext(ctx, `DELETE FROM web_sessions`+activeWebSession, args...)
	default:
		return errors.New("invalid session action")
	}
	if err := requireWebRow(result, err, webadmin.ErrSignedOut); err != nil {
		return err
	}
	return tx.Commit()
}

const activeWebSession = ` WHERE digest = ? AND generation = (SELECT generation FROM web_administrator WHERE id = 1)
    AND issued_ns > ? AND activity_ns > ? AND activity_ns <= ?`

func activeWebSessionArgs(digest [32]byte, now time.Time) []any {
	return []any{digest[:], now.Add(-webadmin.AbsoluteLifetime).UnixNano(), now.Add(-webadmin.IdleLifetime).UnixNano(), now.UnixNano()}
}

func requireActiveWebSession(ctx context.Context, tx *sql.Tx, args []any) error {
	var present int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM web_sessions`+activeWebSession, args...).Scan(&present)
	if errors.Is(err, sql.ErrNoRows) {
		return webadmin.ErrSignedOut
	}
	return err
}

func touchActiveWebSession(ctx context.Context, tx *sql.Tx, now time.Time, args []any) error {
	result, err := tx.ExecContext(ctx, `UPDATE web_sessions SET activity_ns = ?`+activeWebSession, append([]any{now.UnixNano()}, args...)...)
	return requireWebRow(result, err, webadmin.ErrSignedOut)
}

func requireWebRow(result sql.Result, err, errorIfMissing error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	switch affected {
	case 0:
		return errorIfMissing
	case 1:
		return nil
	default:
		return fmt.Errorf("unexpected administrator row count %d", affected)
	}
}
