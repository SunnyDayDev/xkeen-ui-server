package enrollment_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
)

const origin = "https://server.example"

type memoryState struct {
	state       *enrollment.State
	invitation  string
	saveError   error
	inviteReads int
}

func (s *memoryState) Load() (*enrollment.State, error) {
	if s.state == nil {
		return nil, nil
	}
	state := *s.state
	return &state, nil
}
func (s *memoryState) Invitation() (string, error) {
	s.inviteReads++
	return s.invitation, nil
}
func (s *memoryState) Save(state enrollment.State) error {
	if s.saveError != nil {
		return s.saveError
	}
	s.state = &state
	return nil
}

type remote struct {
	enroll   func(context.Context, string, string) (string, error)
	identity func(context.Context, string) (string, error)
}

func (r remote) Enroll(ctx context.Context, invitation, key string) (string, error) {
	return r.enroll(ctx, invitation, key)
}
func (r remote) Identity(ctx context.Context, key string) (string, error) {
	return r.identity(ctx, key)
}

func invite() string { return agentcredential.Generate(agentcredential.Invitation).Encode() }

func TestAgentPersistsBeforeEnrollmentAndChecksTheWorkingKey(t *testing.T) {
	store := &memoryState{invitation: invite()}
	var working string
	server := remote{
		enroll: func(_ context.Context, invitation, key string) (string, error) {
			if _, ok := agentcredential.Parse(key, agentcredential.Working); !ok {
				t.Fatal("invalid working candidate")
			}
			if invitation != store.invitation || store.state == nil || store.state.Key != key ||
				store.state.Origin != origin || store.state.RouterID != "" || store.state.InvitationDigest == "" {
				t.Fatal("candidate was not retained before sending")
			}
			working = key
			return "router-example", nil
		},
		identity: func(_ context.Context, key string) (string, error) {
			if key != working {
				t.Fatal("working key changed between requests")
			}
			return "router-example", nil
		},
	}
	if err := enrollment.Connect(t.Context(), origin, store, server); err != nil {
		t.Fatal(err)
	}
	if store.state == nil || store.state.Key != working || store.state.RouterID != "router-example" ||
		store.state.InvitationDigest != "" {
		t.Fatal("confirmed binding was not saved")
	}
}

func TestAgentRecoversTheSameBindingAfterLostResponse(t *testing.T) {
	store := &memoryState{invitation: invite()}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var activated string
	server := remote{
		enroll: func(_ context.Context, _, key string) (string, error) {
			if activated != "" {
				t.Fatal("already activated candidate was enrolled again")
			}
			activated = key
			cancel()
			return "", enrollment.ErrTemporary
		},
		identity: func(_ context.Context, key string) (string, error) {
			if key != activated {
				t.Fatal("restart changed the candidate")
			}
			return "router-example", nil
		},
	}
	if err := enrollment.Connect(ctx, origin, store, server); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted startup: %v", err)
	}
	if store.state == nil || store.state.RouterID != "" || store.state.Key != activated {
		t.Fatal("pending binding disappeared")
	}
	store.invitation = ""
	for range 2 {
		if err := enrollment.Connect(t.Context(), origin, store, server); err != nil {
			t.Fatal(err)
		}
	}
	if store.state.RouterID != "router-example" || store.inviteReads != 1 {
		t.Fatal("restart did not use only the saved working key")
	}
}

func TestPendingReplayUsesOnlyTheOriginalInvitationAndCandidate(t *testing.T) {
	store := &memoryState{invitation: invite()}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	initial := remote{enroll: func(_ context.Context, _, _ string) (string, error) {
		cancel()
		return "", enrollment.ErrTemporary
	}}
	if err := enrollment.Connect(ctx, origin, store, initial); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	originalKey, originalInvite := store.state.Key, store.invitation
	active := false
	server := remote{
		identity: func(_ context.Context, key string) (string, error) {
			if key != originalKey {
				t.Fatal("candidate changed")
			}
			if !active {
				return "", enrollment.ErrUnauthorized
			}
			return "router-example", nil
		},
		enroll: func(_ context.Context, invitation, key string) (string, error) {
			if invitation != originalInvite || key != originalKey {
				t.Fatal("replay changed invitation or working candidate")
			}
			active = true
			return "router-example", nil
		},
	}
	store.invitation = invite()
	if err := enrollment.Connect(t.Context(), origin, store, server); !errors.Is(err, enrollment.ErrInvitation) || active {
		t.Fatalf("changed invitation was used: %v", err)
	}
	store.invitation = originalInvite
	if err := enrollment.Connect(t.Context(), origin, store, server); err != nil {
		t.Fatal(err)
	}
}

func TestAgentRejectsLocalFailureAndWrongOriginBeforeNetwork(t *testing.T) {
	key := agentcredential.Generate(agentcredential.Working).Encode()
	store := &memoryState{invitation: invite(), saveError: errors.New("private fixture")}
	if err := enrollment.Connect(t.Context(), origin, store, remote{}); !errors.Is(err, enrollment.ErrState) {
		t.Fatalf("local write error: %v", err)
	}
	store.state = &enrollment.State{Origin: origin, Key: key, RouterID: "router-example"}
	if err := enrollment.Connect(t.Context(), "https://other.example", store, remote{}); !errors.Is(err, enrollment.ErrOrigin) {
		t.Fatalf("changed origin: %v", err)
	}
	store.state.Key = "invalid"
	if err := enrollment.Connect(t.Context(), origin, store, remote{}); !errors.Is(err, enrollment.ErrState) {
		t.Fatalf("invalid stored working key: %v", err)
	}
}

func TestActiveKeyNeverEnrollsOnRejectionOrDifferentIdentity(t *testing.T) {
	for _, test := range []struct {
		id   string
		err  error
		want error
	}{
		{err: enrollment.ErrUnauthorized, want: enrollment.ErrUnauthorized},
		{id: "another-router", want: enrollment.ErrIdentity},
	} {
		store := &memoryState{state: &enrollment.State{
			Origin: origin, Key: agentcredential.Generate(agentcredential.Working).Encode(), RouterID: "router-example",
		}}
		server := remote{identity: func(context.Context, string) (string, error) {
			return test.id, test.err
		}}
		if err := enrollment.Connect(t.Context(), origin, store, server); !errors.Is(err, test.want) ||
			store.state.RouterID != "router-example" || store.inviteReads != 0 {
			t.Fatalf("active rejection: %v", err)
		}
	}
}

func TestAgentRetriesOnlyTemporaryWorkWithinOneBudget(t *testing.T) {
	store := &memoryState{state: &enrollment.State{
		Origin: origin, Key: agentcredential.Generate(agentcredential.Working).Encode(), RouterID: "router-example",
	}}
	calls := 0
	server := remote{identity: func(context.Context, string) (string, error) {
		calls++
		return "", enrollment.ErrTemporary
	}}
	if err := enrollment.Connect(t.Context(), origin, store, server); !errors.Is(err, enrollment.ErrTemporary) ||
		calls != 3 || store.inviteReads != 0 {
		t.Fatalf("temporary startup: calls=%d err=%v", calls, err)
	}
}

func TestPendingRecoveryRequiresADurableLocalWriteBeforeNetwork(t *testing.T) {
	store := &memoryState{invitation: invite()}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	intoPending := remote{enroll: func(_ context.Context, _, _ string) (string, error) {
		cancel()
		return "", enrollment.ErrTemporary
	}}
	if err := enrollment.Connect(ctx, origin, store, intoPending); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	saved := *store.state
	store.saveError = errors.New("private fixture directory sync failure")
	requests := 0
	server := remote{identity: func(context.Context, string) (string, error) {
		requests++
		return "router-example", nil
	}}
	if err := enrollment.Connect(t.Context(), origin, store, server); !errors.Is(err, enrollment.ErrState) ||
		requests != 0 || *store.state != saved {
		t.Fatalf("recovered pending without durable storage: requests=%d error=%v", requests, err)
	}
}
