package revisionloop

import (
	"context"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

type fakeWatch func(context.Context, string, int64) (bool, error)

func (f fakeWatch) Watch(ctx context.Context, key string, after int64) (bool, error) {
	return f(ctx, key, after)
}

func TestPeriodicReadContinuesWithWatchBlockedAndHintsUseVerifiedCursor(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ticks := make(chan time.Time, 1)
	awaiting := make(chan int64, 2)
	reply := make(chan bool, 1)
	synced := make(chan int64, 2)
	number := int64(1)
	remote := fakeWatch(func(ctx context.Context, _ string, after int64) (bool, error) {
		select {
		case awaiting <- after:
		case <-ctx.Done():
			return false, ctx.Err()
		}
		select {
		case changed := <-reply:
			return changed, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	})
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, "key", 1, ticks, remote, func(context.Context) (*agentrevision.Snapshot, error) {
			number++
			synced <- number
			return &agentrevision.Snapshot{Number: number}, nil
		})
	}()
	if got := <-awaiting; got != 1 {
		t.Fatal(got)
	}
	ticks <- time.Now()
	if got := <-synced; got != 2 {
		t.Fatal(got)
	}
	reply <- true
	if got := <-awaiting; got != 2 {
		t.Fatal(got)
	}
	reply <- true
	if got := <-synced; got != 3 {
		t.Fatal(got)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("missing cancellation")
	}
}
