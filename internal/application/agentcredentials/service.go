// Package agentcredentials manages the lifecycle of a router agent's credentials.
package agentcredentials

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
)

var (
	ErrInvalidRequest    = errors.New("invalid credential request")
	ErrRouterNotReady    = errors.New("router is not ready for enrollment")
	ErrInvalidTransition = errors.New("credential transition is not available")
	ErrUnauthorized      = errors.New("agent credential is unauthorized")
	ErrUnavailable       = errors.New("credential operation is unavailable")
)

type Purpose int

const (
	Initial Purpose = iota + 1
	Replacement
)

const InvitationLifetime = 15 * time.Minute

type Identity struct {
	RouterID   string
	Generation int64
}

// Repository compares proofs and performs atomic transitions for a single router.
type Repository interface {
	Issue(ctx context.Context, routerID string, purpose Purpose, proof agentcredential.Proof, now func() time.Time) error
	Redeem(ctx context.Context, invitation, working agentcredential.Proof, now func() time.Time) (Identity, error)
	Identity(ctx context.Context, proof agentcredential.Proof) (Identity, error)
	Revoke(ctx context.Context, routerID string) error
}

type Service struct {
	repo Repository
	now  func() time.Time
}

func NewService(repo Repository, now func() time.Time) *Service {
	return &Service{repo: repo, now: now}
}

// Issue is for the trusted server composition. External administrative entry
// points must authorize the caller before invoking it.
func (s *Service) Issue(ctx context.Context, routerID string, purpose Purpose) (string, error) {
	if routerID == "" || !utf8.ValidString(routerID) || (purpose != Initial && purpose != Replacement) {
		return "", ErrInvalidRequest
	}
	token := agentcredential.Generate(agentcredential.Invitation)
	if err := s.repo.Issue(ctx, routerID, purpose, token.Proof(), s.now); err != nil {
		return "", safeError(err)
	}
	return token.Encode(), nil
}

func (s *Service) Enroll(ctx context.Context, invitation, working string) (Identity, error) {
	invitationToken, invitationOK := agentcredential.Parse(invitation, agentcredential.Invitation)
	workingToken, workingOK := agentcredential.Parse(working, agentcredential.Working)
	if !invitationOK || !workingOK {
		return Identity{}, ErrUnauthorized
	}
	identity, err := s.repo.Redeem(ctx, invitationToken.Proof(), workingToken.Proof(), s.now)
	if err != nil {
		return Identity{}, safeError(err)
	}
	return identity, nil
}

func (s *Service) Identity(ctx context.Context, working string) (Identity, error) {
	token, ok := agentcredential.Parse(working, agentcredential.Working)
	if !ok {
		return Identity{}, ErrUnauthorized
	}
	identity, err := s.repo.Identity(ctx, token.Proof())
	if err != nil {
		return Identity{}, safeError(err)
	}
	return identity, nil
}

func (s *Service) Revoke(ctx context.Context, routerID string) error {
	if routerID == "" || !utf8.ValidString(routerID) {
		return ErrInvalidRequest
	}
	return safeError(s.repo.Revoke(ctx, routerID))
}

func safeError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{ErrInvalidRequest, ErrRouterNotReady, ErrInvalidTransition, ErrUnauthorized,
		context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrUnavailable
}
