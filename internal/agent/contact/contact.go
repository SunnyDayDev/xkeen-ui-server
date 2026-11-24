// Package contact sends heartbeats and keeps one durable application report pending.
package contact

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

var ErrPending = errors.New("report remains pending")
var ErrState = errors.New("report state is unavailable or invalid")

type Entry struct {
	Origin   string            `json:"origin"`
	RouterID string            `json:"routerId"`
	Sequence int64             `json:"sequence,string"`
	Pending  *reporting.Report `json:"pending,omitempty"`
}

type Store interface {
	LoadReport() (*Entry, error)
	SaveReport(Entry) error
}
type Remote interface {
	Cursor(context.Context, string) (int64, error)
	Contact(context.Context, string, *reporting.Report) error
}
type Queue struct {
	binding enrollment.State
	store   Store
	remote  Remote
	mu      sync.Mutex
	retry   *Entry
}

func New(binding enrollment.State, store Store, remote Remote) *Queue {
	return &Queue{binding: binding, store: store, remote: remote}
}
func (q *Queue) load() (Entry, error) {
	if q.retry != nil {
		return *q.retry, nil
	}
	entry, err := q.store.LoadReport()
	if err != nil {
		return Entry{}, ErrState
	}
	if entry == nil {
		return Entry{Origin: q.binding.Origin, RouterID: q.binding.RouterID}, nil
	}
	if entry.Origin != q.binding.Origin || entry.RouterID != q.binding.RouterID || entry.Sequence < 0 ||
		(entry.Pending != nil && (!entry.Pending.Valid() || entry.Pending.Sequence != entry.Sequence)) {
		return Entry{}, ErrState
	}
	return *entry, nil
}
func (q *Queue) Enqueue(ctx context.Context, report reporting.Report) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	state, err := q.load()
	if err != nil {
		return err
	}
	if state.Pending != nil {
		return ErrPending
	}
	floor, err := q.remote.Cursor(ctx, q.binding.Key)
	if err != nil {
		return err
	}
	current := max(state.Sequence, floor)
	if floor < 0 || current == math.MaxInt64 {
		return ErrState
	}
	report.Sequence = current + 1
	if !report.Valid() {
		return reporting.ErrInvalid
	}
	state.Sequence = report.Sequence
	state.Pending = &report
	return q.store.SaveReport(state)
}
func (q *Queue) Deliver(ctx context.Context) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	state, err := q.load()
	if err != nil || state.Pending == nil {
		return err
	}
	// Re-establish the file and parent directory barrier after any earlier sync failure.
	if err := q.store.SaveReport(state); err != nil {
		return err
	}
	if err := q.remote.Contact(ctx, q.binding.Key, state.Pending); err != nil {
		return err
	}
	confirmed := state
	confirmed.Pending = nil
	if err := q.store.SaveReport(confirmed); err != nil {
		q.retry = &state
		return err
	}
	q.retry = nil
	return nil
}

func (q *Queue) Run(ctx context.Context, onReportError func(error)) {
	var workers sync.WaitGroup
	workers.Go(func() { q.runHeartbeat(ctx, time.Now, wait) })
	workers.Go(func() {
		delay := time.Second
		var notified bool
		for ctx.Err() == nil {
			if err := q.Deliver(ctx); err != nil {
				if ctx.Err() == nil && !notified && onReportError != nil {
					onReportError(err)
				}
				notified = true
				delay = min(2*delay, reporting.Interval)
			} else {
				notified, delay = false, time.Second
			}
			if !wait(ctx, delay) {
				return
			}
		}
	})
	workers.Wait()
}

// RunHeartbeat keeps presence independent when another owner delivers reports.
func (q *Queue) RunHeartbeat(ctx context.Context) {
	q.runHeartbeat(ctx, time.Now, wait)
}

func (q *Queue) runHeartbeat(ctx context.Context, now func() time.Time, pause func(context.Context, time.Duration) bool) {
	delay := time.Second
	for ctx.Err() == nil {
		interval := reporting.Interval
		started := now()
		if err := q.remote.Contact(ctx, q.binding.Key, nil); err != nil {
			interval = delay
			delay = min(delay*2, reporting.Interval)
		} else {
			delay = time.Second
		}
		if !pause(ctx, started.Add(interval).Sub(now())) {
			return
		}
	}
}
func wait(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
