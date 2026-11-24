// Package enrollment authenticates the agent and preserves its local binding.
package enrollment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
)

var (
	ErrState        = errors.New("agent state is unavailable or invalid")
	ErrInvitation   = errors.New("agent invitation is unavailable or invalid")
	ErrOrigin       = errors.New("agent state belongs to another server origin")
	ErrIdentity     = errors.New("server returned an unexpected router identity")
	ErrUnauthorized = errors.New("agent credential was rejected")
	ErrTemporary    = errors.New("agent connection is temporarily unavailable")
	ErrTrust        = errors.New("agent could not verify the HTTPS server")
	ErrProtocol     = errors.New("server did not return a valid agent response")
)

type State struct {
	Origin           string
	Key              string
	RouterID         string
	InvitationDigest string
}

// Store owns the private state for the lifetime of the agent process.
type Store interface {
	Load() (*State, error)
	Invitation() (string, error)
	Save(State) error
}

type Remote interface {
	Enroll(ctx context.Context, invitation, key string) (string, error)
	Identity(ctx context.Context, key string) (string, error)
}

const attempts = 3

// Connect authenticates one registration. Retries reuse the persisted candidate.
func Connect(ctx context.Context, origin string, store Store, remote Remote) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := store.Load()
	if err != nil {
		return ErrState
	}
	var invitation string
	if state == nil {
		invitation, err = readInvitation(store)
		if err != nil {
			return err
		}
		state = &State{
			Origin: origin, Key: agentcredential.Generate(agentcredential.Working).Encode(),
			InvitationDigest: invitationDigest(invitation),
		}
	} else if err := checkStoredState(*state, origin); err != nil {
		return err
	}
	// A prior rename may have succeeded before its directory sync failed.
	// Re-establish the durability barrier when recovering a pending record.
	if state.RouterID == "" {
		if err := store.Save(*state); err != nil {
			return ErrState
		}
	}

	for attempt := range attempts {
		if err := ctx.Err(); err != nil {
			return err
		}
		id, err := connectOnce(ctx, store, remote, *state, invitation)
		if err == nil {
			if state.RouterID != "" {
				return nil
			}
			state.RouterID, state.InvitationDigest = id, ""
			if err := store.Save(*state); err != nil {
				return ErrState
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !errors.Is(err, ErrTemporary) || attempt == attempts-1 {
			return err
		}
		// Once a request may have been sent, check identity before any replay.
		invitation = ""
		timer := time.NewTimer(time.Duration(attempt+1) * 250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ErrTemporary
}

func connectOnce(ctx context.Context, store Store, remote Remote, state State, invitation string) (string, error) {
	if invitation == "" {
		id, err := remote.Identity(ctx, state.Key)
		if err == nil {
			if !validID(id) || (state.RouterID != "" && state.RouterID != id) {
				return "", ErrIdentity
			}
			return id, nil
		}
		if state.RouterID != "" || !errors.Is(err, ErrUnauthorized) {
			return "", err
		}
		invitation, err = readInvitation(store)
		if err != nil || invitationDigest(invitation) != state.InvitationDigest {
			return "", ErrInvitation
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	enrolled, err := remote.Enroll(ctx, invitation, state.Key)
	if err != nil {
		return "", err
	}
	if !validID(enrolled) {
		return "", ErrIdentity
	}
	verified, err := remote.Identity(ctx, state.Key)
	if err != nil {
		return "", err
	}
	if verified != enrolled {
		return "", ErrIdentity
	}
	return verified, nil
}

func checkStoredState(state State, origin string) error {
	if state.Origin != origin {
		return ErrOrigin
	}
	if _, ok := agentcredential.Parse(state.Key, agentcredential.Working); !ok {
		return ErrState
	}
	if state.RouterID != "" {
		if !validID(state.RouterID) || state.InvitationDigest != "" {
			return ErrState
		}
		return nil
	}
	if digest, err := hex.DecodeString(state.InvitationDigest); err != nil || len(digest) != sha256.Size {
		return ErrState
	}
	return nil
}

func readInvitation(store Store) (string, error) {
	invitation, err := store.Invitation()
	if err != nil {
		return "", ErrInvitation
	}
	if _, ok := agentcredential.Parse(invitation, agentcredential.Invitation); !ok {
		return "", ErrInvitation
	}
	return invitation, nil
}

func invitationDigest(invitation string) string {
	digest := sha256.Sum256([]byte(invitation))
	return hex.EncodeToString(digest[:])
}

func validID(id string) bool { return id != "" && utf8.ValidString(id) }
