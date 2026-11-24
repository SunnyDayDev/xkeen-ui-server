package restart

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
)

type restoreFixture struct {
	*applyFixture
	restoreErr  error
	restoreHook func(context.Context, Intent)
	calls       int
}

func (f *restoreFixture) Restore(ctx context.Context, pending Intent, started func(preparation.Runtime) error) (preparation.Baseline, error) {
	f.calls++
	if f.restoreHook != nil {
		f.restoreHook(ctx, pending)
	}
	if f.restoreErr != nil {
		return preparation.Baseline{}, f.restoreErr
	}
	baseline := pending.Previous
	baseline.Runtime.PID, baseline.Runtime.StartTick = 33, 3
	return baseline, started(baseline.Runtime)
}

func pendingFixture(t *testing.T) *restoreFixture {
	f := &restoreFixture{applyFixture: newApplyFixture(t)}
	f.record.Pending = &Intent{AttemptID: "01010101010101010101010101010101", Previous: f.old,
		Target: f.first, TargetFiles: f.old.Files, Phase: FilesVerified}
	return f
}

func TestRestoreUsesDurableIntentAndPreservesLocalBaseline(t *testing.T) {
	f := pendingFixture(t)
	id := f.record.Pending.AttemptID
	if result, err := RestorePending(t.Context(), f, f, "other-attempt"); result != (RollbackResult{}) || !errors.Is(err, ErrPending) || f.calls != 0 {
		t.Fatalf("wrong expected intent: %v, %v", result, err)
	}
	f.restoreHook = func(ctx context.Context, in Intent) {
		if ctx.Err() != nil || in.AttemptID != id || !reflect.DeepEqual(in.Previous, f.old) ||
			f.record.Pending.Rollback == nil || f.record.Pending.Rollback.State != RollbackActive {
			t.Fatal("effect without durable matching rollback authority")
		}
	}
	got, err := RestorePending(t.Context(), f, f, id)
	if err != nil || got.AttemptID != id || got.TargetTag != f.proposed || f.record.Pending != nil ||
		f.record.Current != nil || f.record.LocalBaseline == nil || f.record.LocalBaseline.Runtime.PID != 33 {
		t.Fatalf("initial rollback = %v, %v", got, err)
	}
	got.Replayed = true
	if again, err := RestorePending(t.Context(), f, f, id); err != nil || again != got || f.calls != 1 {
		t.Fatalf("rollback replay = %v, %v, effects=%d", again, err, f.calls)
	}
}

func TestRollbackFailureKeepsPendingAndHistoricalApplied(t *testing.T) {
	f := pendingFixture(t)
	id := f.record.Pending.AttemptID
	f.record.Current = &Confirmed{AttemptID: "previous", Number: 1, Tag: f.proposed, Baseline: f.old}
	current := *f.record.Current
	f.restoreErr = errors.New("private fixture")
	for i := 0; i < 2; i++ {
		got, err := RestorePending(t.Context(), f, f, id)
		if got != (RollbackResult{}) || !errors.Is(err, ErrRollbackFailed) ||
			f.record.Pending == nil || f.record.Pending.Rollback.State != RollbackFailed ||
			!reflect.DeepEqual(current, *f.record.Current) || f.record.LastRollback != nil {
			t.Fatalf("failed rollback = %v, %v", got, err)
		}
	}
	if f.calls != 1 {
		t.Fatal("terminal failure was retried")
	}
	if err := AllowRollbackRetry(f, id); err == nil {
		t.Fatal("failed rollback released the barrier")
	}
}

func TestApplyRollsBackCanceledRequestAndSuppressesIdenticalTarget(t *testing.T) {
	f := &restoreFixture{applyFixture: newApplyFixture(t)}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.installHook = cancel
	f.installErr = context.Canceled
	f.restoreHook = func(ctx context.Context, _ Intent) {
		if ctx.Err() != nil {
			t.Fatal("canceled client canceled restoration")
		}
	}
	service := New(f.scope, f, f, f, f)
	if got, err := service.Apply(ctx, "synthetic", f.proposed); got.AttemptID != "" || !errors.Is(err, ErrRolledBack) || f.calls != 1 {
		t.Fatalf("apply rollback = %v, %v, effects=%d", got.AttemptID, err, f.calls)
	}
	id := f.record.LastRollback.AttemptID
	if _, err := service.Apply(t.Context(), "synthetic", f.proposed); !errors.Is(err, ErrRolledBack) || f.calls != 1 {
		t.Fatal("automatic cycle retried identical target", err)
	}
	if err := AllowRollbackRetry(f, id); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(t.Context(), "synthetic", f.proposed); !errors.Is(err, ErrRolledBack) || f.calls != 2 ||
		f.record.LastRollback.AttemptID == id || f.record.LastRollback.RetryApproved {
		t.Fatal("retry permit was not consumed once", err)
	}
}

func TestRollbackRecordsReopenWithWholeProtectedAuthority(t *testing.T) {
	f := pendingFixture(t)
	dir := privateTestDirectory(t)
	journal, err := OpenJournal(dir, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	id := f.record.Pending.AttemptID
	f.record.Pending.Rollback = &RollbackProgress{State: RollbackActive}
	if err := journal.Save(f.record); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenJournal(dir, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if resumed, err := reopened.Load(); err != nil ||
		resumed.Pending == nil || resumed.Pending.Rollback == nil || resumed.Pending.Rollback.State != RollbackActive ||
		!reflect.DeepEqual(resumed.Pending.Previous.Files, f.old.Files) {
		t.Fatalf("reopened restore authority changed: %v", err)
	}
	if got, err := RestorePending(t.Context(), reopened, f, id); err != nil || got.AttemptID != id {
		t.Fatalf("restore after reopen = %v, %v", got, err)
	}
	if saved, err := reopened.Load(); err != nil || saved.Pending != nil || saved.Current != nil ||
		saved.Working() == nil || saved.Working().Baseline.Runtime.PID != 33 || saved.LastRollback.AttemptID != id {
		t.Fatalf("reopened terminal local proof = %v", err)
	}
}

func TestRestoreDoesNotConfuseHistoricalOutcomeWithNewPending(t *testing.T) {
	f := pendingFixture(t)
	id := f.record.Pending.AttemptID
	got, err := RestorePending(t.Context(), f, f, id)
	if err != nil || got.AttemptID != id {
		t.Fatal(err)
	}
	f.record.Pending = &Intent{AttemptID: "02020202020202020202020202020202"}
	if result, err := RestorePending(t.Context(), f, f, id); result != (RollbackResult{}) || !errors.Is(err, ErrPending) || f.calls != 1 {
		t.Fatal("historical success was returned for a different pending attempt", err)
	}
}

func TestExplicitCancellationLeavesInProgressRollbackResumable(t *testing.T) {
	f := pendingFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.restoreErr = context.Canceled
	f.restoreHook = func(context.Context, Intent) { cancel() }
	id := f.record.Pending.AttemptID
	if _, err := RestorePending(ctx, f, f, id); !errors.Is(err, context.Canceled) ||
		f.record.Pending.Rollback.State != RollbackActive {
		t.Fatalf("interrupted rollback became terminal: %v", err)
	}
	f.restoreErr = nil
	f.restoreHook = nil
	if _, err := RestorePending(t.Context(), f, f, id); err != nil {
		t.Fatalf("startup could not resume the same attempt: %v", err)
	}
}

func TestRollbackFinalJournalFailureDoesNotClaimRestoredRuntime(t *testing.T) {
	f := pendingFixture(t)
	id := f.record.Pending.AttemptID
	f.failCommit = true
	result, err := RestorePending(t.Context(), f, f, id)
	if result != (RollbackResult{}) || !errors.Is(err, ErrJournal) ||
		f.record.LastRollback != nil || f.record.LocalBaseline != nil || f.record.Pending == nil ||
		f.record.Pending.Rollback.State != RollbackActive || f.record.Pending.Rollback.Started == nil {
		t.Fatalf("ambiguous terminal write claimed a restored result: %v, %v", result, err)
	}
}
