package webadmin

import (
	"context"
	"errors"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
)

var ErrEnrollmentMissing = errors.New("prepared router missing")

type EnrollmentState struct {
	RouterID, Name string
	Phase          string
	Epoch, Desired int64
	Expires        time.Time
	Contact        string
	Applied        int64
	Runtime        string
}

var ErrEnrollmentConflict = errors.New("router invitation changed")
var ErrEnrollmentUnavailable = errors.New("router enrollment unavailable")
var ErrInvalidEnrollment = errors.New("invalid router enrollment")

type EnrollmentCommand struct {
	RouterID       string
	Epoch, Desired int64
	Replace        bool
}

type EnrollmentReceipt struct {
	Epoch   int64
	Expires time.Time
	Token   string
}

type RouterEnrollmentStore interface {
	Read(context.Context, [32]byte, func() time.Time, string) (EnrollmentState, error)
	Issue(context.Context, [32]byte, func() time.Time, EnrollmentCommand, agentcredential.Proof) (EnrollmentReceipt, error)
}

type RouterEnrollment struct {
	store RouterEnrollmentStore
	now   func() time.Time
}

func NewRouterEnrollment(store RouterEnrollmentStore, now func() time.Time) RouterEnrollment {
	return RouterEnrollment{store: store, now: now}
}

func (s RouterEnrollment) Status(ctx context.Context, token, id string) (EnrollmentState, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return EnrollmentState{}, err
	}
	return s.store.Read(ctx, digest, s.now, id)
}

func (s RouterEnrollment) Issue(ctx context.Context, token, proof string, command EnrollmentCommand) (EnrollmentReceipt, error) {
	digest, _, err := SessionProof(token, proof, true)
	if err != nil {
		return EnrollmentReceipt{}, err
	}
	invitation := agentcredential.Generate(agentcredential.Invitation)
	receipt, err := s.store.Issue(ctx, digest, s.now, command, invitation.Proof())
	if err != nil {
		return EnrollmentReceipt{}, err
	}
	receipt.Token = invitation.Encode()
	return receipt, nil
}
