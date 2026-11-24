package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type WebRouterEnrollment struct {
	db     *sql.DB
	notify []func(string)
}

func NewWebRouterEnrollment(db *sql.DB, notify ...func(string)) WebRouterEnrollment {
	return WebRouterEnrollment{db: db, notify: notify}
}

func (w WebRouterEnrollment) Read(ctx context.Context, digest [32]byte, clock func() time.Time, id string) (webadmin.EnrollmentState, error) {
	if !validPublicationID(id) {
		return webadmin.EnrollmentState{}, webadmin.ErrEnrollmentMissing
	}
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return webadmin.EnrollmentState{}, err
	}
	defer tx.Rollback()
	now := clock()
	if err := requireActiveWebSession(ctx, tx, activeWebSessionArgs(digest, now)); err != nil {
		return webadmin.EnrollmentState{}, err
	}
	return readWebEnrollment(ctx, tx, now, id)
}

func readWebEnrollment(ctx context.Context, tx *sql.Tx, now time.Time, id string) (webadmin.EnrollmentState, error) {
	active, err := readActivePublication(ctx, tx, id)
	if err != nil {
		return webadmin.EnrollmentState{}, err
	}
	if active == nil {
		return webadmin.EnrollmentState{}, webadmin.ErrEnrollmentMissing
	}
	status, err := readContactStatus(ctx, tx, id, now)
	if err != nil {
		return webadmin.EnrollmentState{}, err
	}
	if status.Desired <= 0 {
		return webadmin.EnrollmentState{}, webadmin.ErrEnrollmentMissing
	}
	state := webadmin.EnrollmentState{
		RouterID: id, Name: active.Draft.Name, Desired: status.Desired,
		Contact: string(status.Presence), Applied: status.Applied, Runtime: status.Runtime, Phase: "missing",
	}
	var registration, expires sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT
		coalesce((SELECT epoch FROM agent_invitation_epochs WHERE router_id = :id), 0),
		(SELECT active FROM agent_registrations WHERE router_id = :id),
		(SELECT expires_ns FROM agent_invitations WHERE router_id = :id AND redeemed_generation IS NULL)`,
		sql.Named("id", id)).Scan(&state.Epoch, &registration, &expires)
	if err != nil {
		return webadmin.EnrollmentState{}, err
	}
	switch {
	case registration.Valid && registration.Int64 == 1:
		state.Phase = "registered"
	case registration.Valid:
		state.Phase = "revoked"
	case expires.Valid:
		state.Expires = time.Unix(0, expires.Int64).UTC()
		state.Phase = "pending"
		if !now.Before(state.Expires) {
			state.Phase = "expired"
		}
	}
	if registration.Valid && expires.Valid {
		state.ReplacementExpires = time.Unix(0, expires.Int64).UTC()
		state.ReplacementPending = now.Before(state.ReplacementExpires)
	}
	return state, nil
}

func (w WebRouterEnrollment) Issue(ctx context.Context, digest [32]byte, clock func() time.Time, command webadmin.EnrollmentCommand, proof agentcredential.Proof) (webadmin.EnrollmentReceipt, error) {
	if !validPublicationID(command.RouterID) || command.Desired < 1 || command.Epoch < 0 {
		return webadmin.EnrollmentReceipt{}, webadmin.ErrInvalidEnrollment
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return webadmin.EnrollmentReceipt{}, err
	}
	defer tx.Rollback()
	now := clock()
	if err := touchActiveWebSession(ctx, tx, now, activeWebSessionArgs(digest, now)); err != nil {
		return webadmin.EnrollmentReceipt{}, err
	}
	state, err := readWebEnrollment(ctx, tx, now, command.RouterID)
	if err != nil {
		return webadmin.EnrollmentReceipt{}, err
	}
	phaseAllows := state.Phase == "missing" && !command.Replace ||
		(state.Phase == "pending" || state.Phase == "expired") && command.Replace
	if !phaseAllows || state.Epoch != command.Epoch || state.Desired != command.Desired {
		return webadmin.EnrollmentReceipt{}, webadmin.ErrEnrollmentConflict
	}
	registration, err := readRegistration(ctx, tx, command.RouterID)
	if err != nil {
		return webadmin.EnrollmentReceipt{}, err
	}
	if !registration.permits(agentcredentials.Initial) {
		return webadmin.EnrollmentReceipt{}, webadmin.ErrEnrollmentConflict
	}
	expires, err := issueCredential(ctx, tx, command.RouterID, agentcredentials.Initial, proof, registration, now)
	if err != nil {
		return webadmin.EnrollmentReceipt{}, err
	}
	if err := commitCredentials(ctx, tx); err != nil {
		return webadmin.EnrollmentReceipt{}, err
	}
	return webadmin.EnrollmentReceipt{Epoch: state.Epoch + 1, Expires: expires}, nil
}

func (w WebRouterEnrollment) Manage(ctx context.Context, digest [32]byte, clock func() time.Time, command webadmin.AgentKeyCommand, proof agentcredential.Proof) (webadmin.EnrollmentReceipt, error) {
	if !validPublicationID(command.RouterID) || command.Desired < 1 || command.Epoch < 0 {
		return webadmin.EnrollmentReceipt{}, webadmin.ErrInvalidEnrollment
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return webadmin.EnrollmentReceipt{}, err
	}
	defer tx.Rollback()
	now := clock()
	if err := touchActiveWebSession(ctx, tx, now, activeWebSessionArgs(digest, now)); err != nil {
		return webadmin.EnrollmentReceipt{}, err
	}
	state, err := readWebEnrollment(ctx, tx, now, command.RouterID)
	if err != nil {
		return webadmin.EnrollmentReceipt{}, err
	}
	if state.Epoch != command.Epoch || state.Desired != command.Desired {
		return webadmin.EnrollmentReceipt{}, webadmin.ErrEnrollmentConflict
	}
	var expires time.Time
	switch command.Action {
	case webadmin.RevokeAgentKey:
		if state.Phase != "registered" && (state.Phase != "revoked" || state.ReplacementExpires.IsZero()) {
			return webadmin.EnrollmentReceipt{}, webadmin.ErrEnrollmentConflict
		}
		if err := revokeCredentials(ctx, tx, command.RouterID); err != nil {
			return webadmin.EnrollmentReceipt{}, err
		}
	case webadmin.RotateAgentKey, webadmin.RecoverAgentKey:
		if (command.Action == webadmin.RotateAgentKey && state.Phase != "registered") ||
			(command.Action == webadmin.RecoverAgentKey && state.Phase != "revoked") {
			return webadmin.EnrollmentReceipt{}, webadmin.ErrEnrollmentConflict
		}
		registration, err := readRegistration(ctx, tx, command.RouterID)
		if err != nil {
			return webadmin.EnrollmentReceipt{}, err
		}
		if !registration.permits(agentcredentials.Replacement) {
			return webadmin.EnrollmentReceipt{}, webadmin.ErrEnrollmentConflict
		}
		expires, err = issueCredential(ctx, tx, command.RouterID, agentcredentials.Replacement, proof, registration, now)
		if err != nil {
			return webadmin.EnrollmentReceipt{}, err
		}
	default:
		return webadmin.EnrollmentReceipt{}, webadmin.ErrInvalidEnrollment
	}
	if err := commitCredentials(ctx, tx); err != nil {
		return webadmin.EnrollmentReceipt{}, err
	}
	if command.Action == webadmin.RevokeAgentKey {
		for _, notify := range w.notify {
			notify(command.RouterID)
		}
	}
	return webadmin.EnrollmentReceipt{Epoch: state.Epoch + 1, Expires: expires}, nil
}
