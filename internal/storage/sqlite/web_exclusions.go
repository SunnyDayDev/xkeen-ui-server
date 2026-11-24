package sqlite

import (
	"context"
	"database/sql"
	jsonv2 "encoding/json/v2"
	"errors"
	"math"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/google/uuid"
)

const createSharedExclusions = `CREATE TABLE shared_xkeen_exclusions (
 template_id TEXT PRIMARY KEY NOT NULL COLLATE BINARY REFERENCES templates(id),
 element_id TEXT NOT NULL COLLATE BINARY,
 version INTEGER NOT NULL CHECK(typeof(version)='integer' AND version>0),
 source TEXT NOT NULL,
 variables BLOB NOT NULL
);
CREATE TABLE accepted_xkeen_exclusions (
 template_id TEXT NOT NULL COLLATE BINARY,
 template_version INTEGER NOT NULL,
 element_id TEXT NOT NULL,
 source TEXT NOT NULL,
 variables BLOB NOT NULL,
 PRIMARY KEY(template_id,template_version)
);
CREATE TRIGGER accept_xkeen_exclusions AFTER INSERT ON accepted_templates
BEGIN
 INSERT INTO accepted_xkeen_exclusions(template_id,template_version,element_id,source,variables)
 SELECT NEW.template_id,NEW.version,e.element_id,e.source,e.variables
 FROM shared_xkeen_exclusions e WHERE e.template_id=NEW.template_id;
END`

func readExclusions(ctx context.Context, tx *sql.Tx, id string) (webadmin.ExclusionDraft, error) {
	var s webadmin.ExclusionDraft
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT coalesce(e.element_id,''),coalesce(e.version,0),coalesce(e.source,''),coalesce(e.variables,'[]')
 FROM templates t LEFT JOIN shared_xkeen_exclusions e ON e.template_id=t.id WHERE t.id=?`, id).Scan(&s.ID, &s.Version, &s.Text, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return s, templates.ErrNotFound
	}
	if err != nil {
		return s, err
	}
	if jsonv2.Unmarshal(raw, &s.Variables, jsonv2.RejectUnknownMembers(true)) != nil || !s.Valid() || s.Version != 0 && !configuration.ValidElementID(s.ID) {
		return webadmin.ExclusionDraft{}, templates.ErrCorrupt
	}
	return s, nil
}

func (w WebTemplates) ReadExclusions(ctx context.Context, session [32]byte, clock func() time.Time, id string) (webadmin.ExclusionDraft, error) {
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return webadmin.ExclusionDraft{}, err
	}
	defer tx.Rollback()
	if err = requireActiveWebSession(ctx, tx, activeWebSessionArgs(session, clock())); err != nil {
		return webadmin.ExclusionDraft{}, err
	}
	return readExclusions(ctx, tx, id)
}

func (w WebTemplates) SaveExclusions(ctx context.Context, session [32]byte, clock func() time.Time, id string, s webadmin.ExclusionDraft) (webadmin.ExclusionDraft, error) {
	var zero webadmin.ExclusionDraft
	if !s.Valid() || s.Version < 0 {
		return zero, templates.ErrInvalid
	}
	raw, err := jsonv2.Marshal(s.Variables)
	if err != nil {
		return zero, err
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	now := clock()
	if err = touchActiveWebSession(ctx, tx, now, activeWebSessionArgs(session, now)); err != nil {
		return zero, err
	}
	old, err := readExclusions(ctx, tx, id)
	if err != nil {
		return zero, err
	}
	if s.Version != old.Version || s.ID != old.ID || s.Version == math.MaxInt64 {
		return zero, templates.ErrVersionConflict
	}
	if old.Version == 0 {
		key, err := uuid.NewRandom()
		if err != nil {
			return zero, err
		}
		s.ID = key.String()
		_, err = tx.ExecContext(ctx, `INSERT INTO shared_xkeen_exclusions(template_id,element_id,version,source,variables) VALUES(?,?,1,?,?)`, id, s.ID, s.Text, raw)
		if err != nil {
			return zero, err
		}
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE shared_xkeen_exclusions SET version=version+1,source=?,variables=? WHERE template_id=? AND version=?`, s.Text, raw, id, old.Version)
		if err != nil {
			return zero, err
		}
	}
	// A changed exclusion source invalidates every pending publication or editor using the shared version.
	result, err := tx.ExecContext(ctx, "UPDATE templates SET version=version+1 WHERE id=? AND version<?", id, int64(math.MaxInt64))
	if err != nil {
		return zero, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return zero, err
	}
	if affected != 1 {
		return zero, templates.ErrVersionExhausted
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	s.Version++
	return s, nil
}

func publicationExclusions(ctx context.Context, tx *sql.Tx, id string, acceptedVersion *int64) (*configuration.ExclusionSource, error) {
	var elementID, text string
	var raw []byte
	var err error
	if acceptedVersion == nil {
		err = tx.QueryRowContext(ctx, "SELECT element_id,source,variables FROM shared_xkeen_exclusions WHERE template_id=?", id).Scan(&elementID, &text, &raw)
	} else {
		err = tx.QueryRowContext(ctx, "SELECT element_id,source,variables FROM accepted_xkeen_exclusions WHERE template_id=? AND template_version=?", id, *acceptedVersion).Scan(&elementID, &text, &raw)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	draft := webadmin.ExclusionDraft{Text: text}
	if jsonv2.Unmarshal(raw, &draft.Variables, jsonv2.RejectUnknownMembers(true)) != nil || !draft.Valid() {
		return nil, templates.ErrCorrupt
	}
	source := draft.Source()
	source.ElementID = elementID
	return &source, nil
}
