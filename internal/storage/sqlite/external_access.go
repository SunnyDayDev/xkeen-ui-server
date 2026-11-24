package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
)

const createExternalAccess = `
 CREATE TABLE external_access (
 id INTEGER PRIMARY KEY CHECK(id=1),
 enabled INTEGER NOT NULL CHECK(enabled IN(0,1)),
 protected INTEGER NOT NULL CHECK(protected IN(0,1)),
 version INTEGER NOT NULL CHECK(typeof(version)='integer' AND version>0),
 digest BLOB CHECK(digest IS NULL OR (typeof(digest)='blob' AND length(digest)=32))
 );
 INSERT INTO external_access(id,enabled,protected,version) VALUES(1,0,1,1);`

type ExternalAccess struct {
	db  *sql.DB
	now func() time.Time
}

func NewExternalAccess(db *sql.DB, now func() time.Time) *ExternalAccess {
	return &ExternalAccess{db, now}
}

type rowReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readExternalSettings(ctx context.Context, r rowReader) (externalaccess.Settings, []byte, error) {
	var s externalaccess.Settings
	var digest []byte
	err := r.QueryRowContext(ctx, "SELECT enabled,protected,version,digest FROM external_access WHERE id=1").Scan(&s.Enabled, &s.Protected, &s.Version, &digest)
	s.HasToken = len(digest) == 32
	return s, digest, err
}

func (s *ExternalAccess) Admit(ctx context.Context, headers []string) (externalaccess.Permit, error) {
	v, digest, err := readExternalSettings(ctx, s.db)
	if err != nil {
		return externalaccess.Permit{}, err
	}
	return externalaccess.Admit(v, digest, headers)
}

func (s *ExternalAccess) Owner(ctx context.Context, session [32]byte, change *externalaccess.Change) (externalaccess.Settings, string, error) {
	var zero externalaccess.Settings
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: change == nil})
	if err != nil {
		return zero, "", err
	}
	defer tx.Rollback()
	if err := requireActiveWebSession(ctx, tx, activeWebSessionArgs(session, s.now())); err != nil {
		return zero, "", err
	}
	v, _, err := readExternalSettings(ctx, tx)
	if err != nil {
		return zero, "", err
	}
	if change == nil {
		return v, "", nil
	}
	if change.Version != v.Version {
		return zero, "", externalaccess.ErrConflict
	}
	var secret string
	if change.Rotate {
		token, digest, err := externalaccess.Issue()
		if err != nil {
			return zero, "", err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE external_access SET digest=?,version=version+1 WHERE id=1", digest[:]); err != nil {
			return zero, "", err
		}
		secret = token
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE external_access SET enabled=?,protected=?,version=version+1 WHERE id=1", change.Enabled, change.Protected)
		if err != nil {
			return zero, "", err
		}
	}
	v, _, err = readExternalSettings(ctx, tx)
	if err != nil {
		return zero, "", err
	}
	if err = tx.Commit(); err != nil {
		return zero, "", err
	}
	return v, secret, nil
}
