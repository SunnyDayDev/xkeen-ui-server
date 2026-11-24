package revisionwatch

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
)

type reader func(context.Context, agentcredentials.Identity) (int64, error)

func (f reader) ReadWatch(ctx context.Context, id agentcredentials.Identity) (int64, error) {
	return f(ctx, id)
}
func TestWaitRegistersBeforeReadingAndChecksAgain(t *testing.T) {
	id := agentcredentials.Identity{RouterID: "router.example", Generation: 1}
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	w := New(t.Context(), reader(func(context.Context, agentcredentials.Identity) (int64, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return 1, nil
		}
		return 2, nil
	}), time.Second, 2)
	result := make(chan error, 1)
	go func() {
		changed, err := w.Wait(t.Context(), id, 1)
		if !changed && err == nil {
			err = errors.New("missing change")
		}
		result <- err
	}()
	<-entered
	if _, err := w.Wait(t.Context(), id, 1); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	w.Notify("unrelated.example")
	w.Notify(id.RouterID)
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
func TestWaitRechecksRevocationAndStopsOnShutdown(t *testing.T) {
	id := agentcredentials.Identity{RouterID: "router.example", Generation: 1}
	for _, shutdown := range []bool{false, true} {
		ctx, stop := context.WithCancel(t.Context())
		var revoked atomic.Bool
		entered := make(chan struct{})
		w := New(ctx, reader(func(context.Context, agentcredentials.Identity) (int64, error) {
			if revoked.Load() {
				return 0, agentcredentials.ErrUnauthorized
			}
			close(entered)
			return 1, nil
		}), time.Second, 1)
		result := make(chan error, 1)
		go func() { _, err := w.Wait(t.Context(), id, 1); result <- err }()
		<-entered
		want := agentcredentials.ErrUnauthorized
		if shutdown {
			stop()
			want = ErrUnavailable
		} else {
			revoked.Store(true)
			w.Notify(id.RouterID)
		}
		if err := <-result; !errors.Is(err, want) {
			t.Fatalf("got %v want %v", err, want)
		}
		stop()
	}
}
func TestWaitChecksAtTimeout(t *testing.T) {
	var calls int
	w := New(t.Context(), reader(func(context.Context, agentcredentials.Identity) (int64, error) { calls++; return 1, nil }), time.Millisecond, 1)
	if changed, err := w.Wait(t.Context(), agentcredentials.Identity{RouterID: "router.example", Generation: 1}, 1); changed || err != nil || calls != 2 {
		t.Fatalf("%v %v %d", changed, err, calls)
	}
}
