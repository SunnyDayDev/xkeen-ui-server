package contact

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

type memoryQueue struct {
	state *Entry
	err   error
}

func (m *memoryQueue) LoadReport() (*Entry, error) { return m.state, nil }
func (m *memoryQueue) SaveReport(v Entry) error {
	if m.err != nil {
		return m.err
	}
	m.state = &v
	return nil
}

type remoteContact struct {
	floor int64
	sent  []reporting.Report
	err   error
}

func (r *remoteContact) Cursor(context.Context, string) (int64, error) { return r.floor, nil }
func (r *remoteContact) Contact(_ context.Context, _ string, report *reporting.Report) error {
	if report != nil {
		r.sent = append(r.sent, *report)
	}
	return r.err
}
func TestPendingReplayAndAuthenticatedFloor(t *testing.T) {
	b := enrollment.State{Origin: "https://example.net", RouterID: "router-example", Key: "synthetic"}
	store := &memoryQueue{}
	remote := &remoteContact{floor: 40, err: reporting.ErrUnavailable}
	queue := New(b, store, remote)
	event := reporting.Report{Revision: 2, State: "applied"}
	if err := queue.Enqueue(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if store.state.Pending.Sequence != 41 {
		t.Fatal("floor not used")
	}
	if !errors.Is(queue.Deliver(t.Context()), reporting.ErrUnavailable) {
		t.Fatal("expected unavailable")
	}
	queue = New(b, store, remote)
	store.err = errors.New("sync failed")
	if queue.Deliver(t.Context()) == nil || len(remote.sent) != 1 {
		t.Fatal("network bypassed durability barrier")
	}
	store.err = nil
	remote.err = nil
	remote.floor = 50
	if err := queue.Deliver(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(remote.sent) != 2 || remote.sent[0] != remote.sent[1] || store.state.Pending != nil {
		t.Fatal("pending not replayed exactly")
	}
	if err := queue.Enqueue(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if store.state.Pending.Sequence != 51 {
		t.Fatal("server floor not restored")
	}
}

type concurrentRemote struct {
	blocked   chan struct{}
	heartbeat chan struct{}
}

func (r concurrentRemote) Cursor(context.Context, string) (int64, error) { return 0, nil }

func (r concurrentRemote) Contact(ctx context.Context, _ string, report *reporting.Report) error {
	if report == nil {
		r.heartbeat <- struct{}{}
		return nil
	}
	close(r.blocked)
	<-ctx.Done()
	return ctx.Err()
}

func TestBlockedReportDoesNotDelayHeartbeat(t *testing.T) {
	b := enrollment.State{Origin: "https://example.net", RouterID: "router-example", Key: "synthetic"}
	remote := concurrentRemote{blocked: make(chan struct{}), heartbeat: make(chan struct{}, 2)}
	queue := New(b, &memoryQueue{}, remote)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := queue.Enqueue(ctx, reporting.Report{Revision: 1, State: "applying"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- queue.Deliver(ctx) }()
	select {
	case <-remote.blocked:
	case <-time.After(time.Second):
		t.Fatal("delivery did not start")
	}
	now := time.Unix(100, 0)
	var periods []time.Duration
	queue.runHeartbeat(ctx, func() time.Time { return now }, func(_ context.Context, interval time.Duration) bool {
		periods = append(periods, interval)
		now = now.Add(interval)
		return len(periods) < 2
	})
	if len(remote.heartbeat) != 2 || len(periods) != 2 || periods[0] != 60*time.Second || periods[1] != 60*time.Second {
		t.Fatalf("heartbeat schedule: %v", periods)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked delivery: %v", err)
	}
}

type visibleClearFailure struct {
	memoryQueue
	fail bool
}

func (m *visibleClearFailure) SaveReport(state Entry) error {
	m.state = &state
	if state.Pending == nil && m.fail {
		return ErrState
	}
	return nil
}
func TestQueueDoesNotForgetPendingWhenClearRenameBecameVisible(t *testing.T) {
	b := enrollment.State{Origin: "https://example.net", RouterID: "router-example", Key: "synthetic"}
	store := &visibleClearFailure{fail: true}
	remote := &remoteContact{}
	queue := New(b, store, remote)
	event := reporting.Report{Revision: 2, State: "applied"}
	if err := queue.Enqueue(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if err := queue.Deliver(t.Context()); !errors.Is(err, ErrState) {
		t.Fatalf("clear: %v", err)
	}
	if err := queue.Enqueue(t.Context(), event); !errors.Is(err, ErrPending) {
		t.Fatalf("pending lost: %v", err)
	}
	store.fail = false
	if err := queue.Deliver(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(remote.sent) != 2 || remote.sent[0] != remote.sent[1] {
		t.Fatal("missing exact retry")
	}
}
