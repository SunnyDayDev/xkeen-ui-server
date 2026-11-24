package webadmin

import (
	"context"
	"errors"
	"testing"
	"time"
)

type loginTestStore struct {
	Store
	started, release chan struct{}
}

func (s loginTestStore) Verifier(ctx context.Context) (Verifier, int64, error) {
	if s.started != nil {
		close(s.started)
		select {
		case <-s.release:
		case <-ctx.Done():
			return Verifier{}, 0, ctx.Err()
		}
	}
	return Verifier{Version: 1}, 1, nil
}

func TestLoginLimitAndWindow(t *testing.T) {
	now := time.Unix(1000000000, 0)
	service := New(loginTestStore{}, func() time.Time { return now })
	for i := 0; i < 5; i++ {
		if _, _, err := service.Login(t.Context(), []byte("short"), ""); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("failure %d: %v", i, err)
		}
	}
	now = now.Add(30*time.Second - time.Nanosecond)
	if _, _, err := service.Login(t.Context(), []byte("short"), ""); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("limit: %v", err)
	}
	now = now.Add(time.Nanosecond)
	if _, _, err := service.Login(t.Context(), []byte("short"), ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("retry: %v", err)
	}
}

func TestOnlyOneVerificationAtATime(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	service := New(loginTestStore{started: started, release: release}, time.Now)
	finished := make(chan error, 1)
	go func() { _, _, err := service.Login(t.Context(), []byte("short"), ""); finished <- err }()
	defer close(release)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first verification did not start")
	}
	if _, _, err := service.Login(t.Context(), []byte("short"), ""); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("parallel login: %v", err)
	}
}
