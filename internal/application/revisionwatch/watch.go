package revisionwatch

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
)

var (
	ErrBusy        = errors.New("agent watch capacity reached")
	ErrUnavailable = errors.New("agent watch unavailable")
)

type Reader interface {
	ReadWatch(context.Context, agentcredentials.Identity) (int64, error)
}

type Watch struct {
	shutdown context.Context
	reader   Reader
	duration time.Duration
	limit    int
	mu       sync.Mutex
	waiters  map[string]chan struct{}
}

func New(shutdown context.Context, reader Reader, duration time.Duration, limit int) *Watch {
	return &Watch{shutdown: shutdown, reader: reader, duration: duration, limit: limit, waiters: make(map[string]chan struct{})}
}

// Notify coalesces hints by router. The receiver reads the committed state.
func (w *Watch) Notify(routerID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if signal := w.waiters[routerID]; signal != nil {
		select {
		case signal <- struct{}{}:
		default:
		}
	}
}

func (w *Watch) Wait(ctx context.Context, id agentcredentials.Identity, after int64) (bool, error) {
	if w.shutdown.Err() != nil {
		return false, ErrUnavailable
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(w.shutdown, cancel)
	defer stop()
	w.mu.Lock()
	if w.waiters[id.RouterID] != nil || len(w.waiters) >= w.limit {
		w.mu.Unlock()
		return false, ErrBusy
	}
	signal := make(chan struct{}, 1)
	w.waiters[id.RouterID] = signal
	w.mu.Unlock()
	defer func() { w.mu.Lock(); delete(w.waiters, id.RouterID); w.mu.Unlock() }()
	timer := time.NewTimer(w.duration)
	defer timer.Stop()
	timedOut := false
	for {
		number, err := w.reader.ReadWatch(ctx, id)
		if w.shutdown.Err() != nil {
			return false, ErrUnavailable
		}
		if err != nil {
			return false, err
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if number != after || timedOut {
			return number != after, nil
		}
		select {
		case <-ctx.Done():
			if w.shutdown.Err() != nil {
				return false, ErrUnavailable
			}
			return false, ctx.Err()
		case <-signal:
		case <-timer.C:
			timedOut = true
		}
	}
}
