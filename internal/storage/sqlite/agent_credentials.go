package sqlite

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
)

type AgentCredentials struct {
	db *sql.DB
}

var _ agentcredentials.Repository = (*AgentCredentials)(nil)

func NewAgentCredentials(db *sql.DB) *AgentCredentials {
	return &AgentCredentials{db: db}
}

func (s *AgentCredentials) Issue(ctx context.Context, routerID string, purpose agentcredentials.Purpose, proof agentcredential.Proof, now func() time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return credentialDatabaseError(ctx)
	}
	defer tx.Rollback()
	ready, err := credentialRouterReady(ctx, tx, routerID)
	if err != nil {
		return err
	}
	if !ready {
		return agentcredentials.ErrRouterNotReady
	}
	registration, err := readRegistration(ctx, tx, routerID)
	if err != nil {
		return err
	}
	if !registration.permits(purpose) {
		return agentcredentials.ErrInvalidTransition
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_invitations
		WHERE router_id = ? AND redeemed_generation IS NULL`, routerID); err != nil {
		return credentialDatabaseError(ctx)
	}
	expires := now().Add(agentcredentials.InvitationLifetime).UnixNano()
	if err := writeCredential(ctx, tx, `INSERT INTO agent_invitations
		(selector, router_id, purpose, expires_ns, secret_digest, expected_generation)
		VALUES (?, ?, ?, ?, ?, ?)`, proof.Selector[:], routerID, purpose, expires, proof.Digest[:], registration.generation); err != nil {
		return err
	}
	return commitCredentials(ctx, tx)
}

type credentialInvitation struct {
	routerID string
	purpose  agentcredentials.Purpose
	expires  int64
	expected int64
	redeemed sql.NullInt64
}

func (s *AgentCredentials) Redeem(ctx context.Context, invitation, working agentcredential.Proof, now func() time.Time) (agentcredentials.Identity, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return agentcredentials.Identity{}, credentialDatabaseError(ctx)
	}
	defer tx.Rollback()
	record, err := readInvitation(ctx, tx, invitation)
	if err != nil {
		return agentcredentials.Identity{}, err
	}
	if now().UnixNano() >= record.expires {
		return agentcredentials.Identity{}, agentcredentials.ErrUnauthorized
	}
	if record.redeemed.Valid {
		id, err := lookupCredential(ctx, tx, working)
		if err != nil {
			return agentcredentials.Identity{}, err
		}
		if id.RouterID != record.routerID || id.Generation != record.redeemed.Int64 {
			return agentcredentials.Identity{}, agentcredentials.ErrUnauthorized
		}
		if err := commitCredentials(ctx, tx); err != nil {
			return agentcredentials.Identity{}, err
		}
		return id, nil
	}

	id, err := redeemNewCredential(ctx, tx, invitation, working, record)
	if err != nil {
		return agentcredentials.Identity{}, err
	}
	if err := commitCredentials(ctx, tx); err != nil {
		return agentcredentials.Identity{}, err
	}
	return id, nil
}

func readInvitation(ctx context.Context, tx *sql.Tx, proof agentcredential.Proof) (credentialInvitation, error) {
	var record credentialInvitation
	var digest []byte
	err := tx.QueryRowContext(ctx, `SELECT router_id, purpose, expires_ns, secret_digest,
		expected_generation, redeemed_generation FROM agent_invitations WHERE selector = ?`, proof.Selector[:]).
		Scan(&record.routerID, &record.purpose, &record.expires, &digest, &record.expected, &record.redeemed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return credentialInvitation{}, credentialDatabaseError(ctx)
	}
	if err != nil || subtle.ConstantTimeCompare(proof.Digest[:], digest) != 1 {
		return credentialInvitation{}, agentcredentials.ErrUnauthorized
	}
	return record, nil
}

func redeemNewCredential(ctx context.Context, tx *sql.Tx, invitation, working agentcredential.Proof, record credentialInvitation) (agentcredentials.Identity, error) {
	registration, err := readRegistration(ctx, tx, record.routerID)
	if err != nil {
		return agentcredentials.Identity{}, err
	}
	ready, err := credentialRouterReady(ctx, tx, record.routerID)
	if err != nil {
		return agentcredentials.Identity{}, err
	}
	if !ready || registration.generation != record.expected || !registration.permits(record.purpose) {
		return agentcredentials.Identity{}, agentcredentials.ErrUnauthorized
	}
	if registration.generation == math.MaxInt64 {
		return agentcredentials.Identity{}, agentcredentials.ErrUnavailable
	}
	var reused bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM agent_working_keys
		WHERE selector = ? OR secret_digest = ?)`, working.Selector[:], working.Digest[:]).Scan(&reused); err != nil {
		return agentcredentials.Identity{}, credentialDatabaseError(ctx)
	}
	if reused {
		return agentcredentials.Identity{}, agentcredentials.ErrUnauthorized
	}
	generation := registration.generation + 1
	if err := writeCredential(ctx, tx, `INSERT INTO agent_working_keys (selector, router_id, generation, secret_digest)
		VALUES (?, ?, ?, ?)`, working.Selector[:], record.routerID, generation, working.Digest[:]); err != nil {
		return agentcredentials.Identity{}, err
	}
	if err := writeCredential(ctx, tx, `INSERT INTO agent_registrations (router_id, generation, active)
		VALUES (?, ?, 1) ON CONFLICT(router_id) DO UPDATE SET generation = excluded.generation, active = 1`,
		record.routerID, generation); err != nil {
		return agentcredentials.Identity{}, err
	}
	if err := writeCredential(ctx, tx, `UPDATE agent_invitations SET redeemed_generation = ? WHERE selector = ?`,
		generation, invitation.Selector[:]); err != nil {
		return agentcredentials.Identity{}, err
	}
	return agentcredentials.Identity{RouterID: record.routerID, Generation: generation}, nil
}

func (s *AgentCredentials) Identity(ctx context.Context, proof agentcredential.Proof) (agentcredentials.Identity, error) {
	return lookupCredential(ctx, s.db, proof)
}

func lookupCredential(ctx context.Context, source interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, proof agentcredential.Proof) (agentcredentials.Identity, error) {
	var id agentcredentials.Identity
	var digest []byte
	err := source.QueryRowContext(ctx, `SELECT k.router_id, k.generation, k.secret_digest
		FROM agent_working_keys AS k JOIN agent_registrations AS r ON r.router_id = k.router_id
		AND r.generation = k.generation AND r.active = 1 WHERE k.selector = ?`, proof.Selector[:]).
		Scan(&id.RouterID, &id.Generation, &digest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return agentcredentials.Identity{}, credentialDatabaseError(ctx)
	}
	if err != nil || subtle.ConstantTimeCompare(proof.Digest[:], digest) != 1 {
		return agentcredentials.Identity{}, agentcredentials.ErrUnauthorized
	}
	return id, nil
}

func (s *AgentCredentials) Revoke(ctx context.Context, routerID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return credentialDatabaseError(ctx)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE agent_registrations SET active = 0 WHERE router_id = ?`, routerID); err != nil {
		return credentialDatabaseError(ctx)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_invitations
		WHERE router_id = ? AND redeemed_generation IS NULL`, routerID); err != nil {
		return credentialDatabaseError(ctx)
	}
	var outstanding bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM agent_registrations
		WHERE router_id = ? AND active = 1) OR EXISTS (SELECT 1 FROM agent_invitations
		WHERE router_id = ? AND redeemed_generation IS NULL)`, routerID, routerID).Scan(&outstanding)
	if err != nil || outstanding {
		return credentialDatabaseError(ctx)
	}
	return commitCredentials(ctx, tx)
}

type credentialRegistration struct {
	generation int64
}

func (record credentialRegistration) permits(purpose agentcredentials.Purpose) bool {
	return (purpose == agentcredentials.Initial && record.generation == 0) ||
		(purpose == agentcredentials.Replacement && record.generation > 0)
}

func readRegistration(ctx context.Context, tx *sql.Tx, routerID string) (credentialRegistration, error) {
	var record credentialRegistration
	err := tx.QueryRowContext(ctx, `SELECT generation FROM agent_registrations WHERE router_id = ?`, routerID).
		Scan(&record.generation)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return credentialRegistration{}, credentialDatabaseError(ctx)
	}
	return record, nil
}

func credentialRouterReady(ctx context.Context, tx *sql.Tx, routerID string) (bool, error) {
	var ready bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM active_router_publications AS a
		WHERE a.router_id = ? AND EXISTS (SELECT 1 FROM desired_router_revisions r WHERE r.router_id = a.router_id))`, routerID).Scan(&ready)
	if err != nil {
		return false, credentialDatabaseError(ctx)
	}
	return ready, nil
}

func writeCredential(ctx context.Context, tx *sql.Tx, statement string, args ...any) error {
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return credentialDatabaseError(ctx)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return credentialDatabaseError(ctx)
	}
	return nil
}

func commitCredentials(ctx context.Context, tx *sql.Tx) error {
	if err := tx.Commit(); err != nil {
		return credentialDatabaseError(ctx)
	}
	return nil
}

func credentialDatabaseError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return agentcredentials.ErrUnavailable
}
