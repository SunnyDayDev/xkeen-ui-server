package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

const createContactStorage = `CREATE TABLE agent_contacts (
 router_id TEXT NOT NULL PRIMARY KEY COLLATE BINARY,
 contact_ns INTEGER NOT NULL,
 report BLOB,
 applied INTEGER NOT NULL CHECK(applied >= 0),
 uncertain INTEGER NOT NULL CHECK(uncertain >= 0)
)`

type Contacts struct {
	db  *sql.DB
	now func() time.Time
}

func NewContacts(db *sql.DB, now func() time.Time) *Contacts { return &Contacts{db: db, now: now} }

type contactRecord struct {
	ns                 int64
	last               *reporting.Report
	applied, uncertain int64
	observed           bool
}

func loadContact(ctx context.Context, tx *sql.Tx, id string) (contactRecord, error) {
	var p contactRecord
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT contact_ns,report,applied,uncertain FROM agent_contacts WHERE router_id=?`, id).Scan(&p.ns, &raw, &p.applied, &p.uncertain)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, reporting.ErrUnavailable
	}
	if raw != nil {
		var r reporting.Report
		if json.Unmarshal(raw, &r) != nil || !r.Valid() {
			return p, reporting.ErrUnavailable
		}
		p.last = &r
	}
	p.observed = true
	return p, nil
}

func requireContactIdentity(ctx context.Context, tx *sql.Tx, id agentcredentials.Identity) error {
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_registrations WHERE router_id=? AND generation=? AND active=1)`, id.RouterID, id.Generation).Scan(&active); err != nil {
		return reporting.ErrUnavailable
	}
	if !active {
		return agentcredentials.ErrUnauthorized
	}
	return nil
}

func (c *Contacts) Accept(ctx context.Context, id agentcredentials.Identity, report *reporting.Report) error {
	if report != nil && !report.Valid() {
		return reporting.ErrInvalid
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return reporting.ErrUnavailable
	}
	defer tx.Rollback()
	if err := requireContactIdentity(ctx, tx, id); err != nil {
		return err
	}
	state, err := loadContact(ctx, tx, id.RouterID)
	if err != nil {
		return err
	}
	if report != nil {
		if err := acceptReport(ctx, tx, id.RouterID, &state, *report); err != nil {
			return err
		}
	}
	var raw []byte
	if state.last != nil {
		raw, _ = json.Marshal(state.last)
	}
	now := c.now().UnixNano()
	result, err := tx.ExecContext(ctx, `INSERT INTO agent_contacts(router_id,contact_ns,report,applied,uncertain) VALUES (?,?,?,?,?)
 ON CONFLICT(router_id) DO UPDATE SET contact_ns=excluded.contact_ns,report=excluded.report,applied=excluded.applied,uncertain=excluded.uncertain`, id.RouterID, now, raw, state.applied, state.uncertain)
	if err != nil {
		return reporting.ErrUnavailable
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return reporting.ErrUnavailable
	}
	if tx.Commit() != nil {
		return reporting.ErrUnavailable
	}
	return nil
}

func acceptReport(ctx context.Context, tx *sql.Tx, id string, p *contactRecord, r reporting.Report) error {
	advance, err := reporting.Advances(p.last, r)
	if err != nil || !advance {
		return err
	}
	var known bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM desired_router_revisions WHERE router_id=? AND revision=?)`, id, r.Revision).Scan(&known); err != nil {
		return reporting.ErrUnavailable
	}
	if !known {
		return reporting.ErrUnknownRevision
	}
	evidence := (reporting.Evidence{Applied: p.applied, Uncertain: p.uncertain}).After(r)
	p.applied, p.uncertain = evidence.Applied, evidence.Uncertain
	p.last = &r
	return nil
}

func (c *Contacts) Cursor(ctx context.Context, id agentcredentials.Identity) (int64, error) {
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, reporting.ErrUnavailable
	}
	defer tx.Rollback()
	if err := requireContactIdentity(ctx, tx, id); err != nil {
		return 0, err
	}
	state, err := loadContact(ctx, tx, id.RouterID)
	if err != nil {
		return 0, err
	}
	if state.last == nil {
		return 0, nil
	}
	return state.last.Sequence, nil
}

func (c *Contacts) Status(ctx context.Context, id string) (reporting.Status, error) {
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return reporting.Status{}, reporting.ErrUnavailable
	}
	defer tx.Rollback()
	state, err := loadContact(ctx, tx, id)
	if err != nil {
		return reporting.Status{}, err
	}
	status := reporting.Status{Presence: reporting.Unobserved, Applied: state.applied, Last: state.last}
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(revision),0) FROM desired_router_revisions WHERE router_id=?`, id).Scan(&status.Desired); err != nil {
		return reporting.Status{}, reporting.ErrUnavailable
	}
	if state.observed {
		status.LastContact = time.Unix(0, state.ns)
		status.Presence = reporting.Online
		if c.now().Sub(status.LastContact) >= reporting.OfflineAfter {
			status.Presence = reporting.Offline
		}
	}
	if state.uncertain > 0 {
		status.Runtime = "unknown"
	} else if state.applied > 0 {
		status.Runtime = "verified"
	}
	return status, nil
}
