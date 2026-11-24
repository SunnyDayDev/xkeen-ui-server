package webadmin

import (
	"context"
	"errors"
	"time"
)

var (
	ErrSetupRequired = errors.New("administrator setup required")
	ErrAlreadySetup  = errors.New("administrator already initialized")
	ErrSignedOut     = errors.New("session inactive")
)

const (
	IdleLifetime     = 30 * time.Minute
	AbsoluteLifetime = 12 * time.Hour
)

type SessionAction uint8

const (
	SessionRead SessionAction = iota
	SessionTouch
	SessionRevoke
)

type Store interface {
	SaveVerifier(context.Context, Verifier, bool) error
	Verifier(context.Context) (Verifier, int64, error)
	Issue(context.Context, int64, [32]byte, [32]byte, time.Time) error
	Session(context.Context, [32]byte, func() time.Time, SessionAction) error
}
