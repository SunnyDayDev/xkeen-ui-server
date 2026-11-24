// Package revisionloop coalesces watch hints and periodic desired reads.
package revisionloop

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

type Remote interface {
	Watch(context.Context, string, int64) (bool, error)
}

type watchResult struct {
	after   int64
	changed bool
	err     error
}

// Run performs one reconciliation at a time while an independent watch waits.
func Run(ctx context.Context, key string, after int64, ticks <-chan time.Time, remote Remote, syncDesired func(context.Context) (*agentrevision.Snapshot, error)) error {
	ctx, cancel := context.WithCancel(ctx)
	results := make(chan watchResult, 1)
	var done chan struct{}
	defer func() {
		cancel()
		if done != nil {
			<-done
		}
	}()
	start := func(number int64, delay time.Duration) {
		done = make(chan struct{})
		go func(finished chan struct{}) {
			defer close(finished)
			if delay > 0 {
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-ctx.Done():
					return
				}
			}
			changed, err := remote.Watch(ctx, key, number)
			results <- watchResult{number, changed, err}
		}(done)
	}
	reconcile := func() error {
		snapshot, err := syncDesired(ctx)
		if err != nil {
			return err
		}
		after = 0
		if snapshot != nil {
			after = snapshot.Number
		}
		return nil
	}
	backoff := time.Second
	recovering := false
	start(after, 0)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, open := <-ticks:
			if !open {
				ticks = nil
				continue
			}
			if err := reconcile(); err != nil && !retryable(err) {
				return err
			}
		case outcome := <-results:
			<-done
			done = nil
			err := outcome.err
			if err == nil && (recovering || outcome.changed && outcome.after == after) {
				err = reconcile()
			}
			if err != nil {
				if !retryable(err) {
					return err
				}
				recovering = true
				delay := backoff - time.Duration(rand.Int64N(int64(backoff/4)))
				backoff = min(2*backoff, time.Minute)
				start(after, delay)
				continue
			}
			recovering = false
			backoff = time.Second
			start(after, 0)
		}
	}
}

func retryable(err error) bool {
	return errors.Is(err, enrollment.ErrTemporary) || errors.Is(err, enrollment.ErrProtocol)
}
