package restart

import (
	"context"
	"errors"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
)

type startupWorking struct {
	*restoreFixture
	starts     int
	proofError error
}

func (w *startupWorking) StartCommitted(_ context.Context, journal Journal) error {
	w.starts++
	record, err := journal.Load()
	if err != nil {
		return err
	}
	if record.Pending != nil {
		return ErrPending
	}
	return w.proofError
}

type startupJournal struct {
	Journal
	readError error
}

func (j startupJournal) Load() (Record, error) {
	if j.readError != nil {
		return Record{}, j.readError
	}
	return j.Journal.Load()
}

func TestStartupRecoveryMatrix(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		phase                             Phase
		pending, committed, fail, corrupt bool
		restores, starts                  int
		want                              error
	}{
		{name: "1-before-intent", starts: 1},
		{name: "2-early-ambiguous-intent", pending: true, phase: Planned, restores: 1, starts: 1},
		{name: "3-interrupted-replacement", pending: true, phase: Stopped, restores: 1, starts: 1},
		{name: "4-unverified-runtime", pending: true, phase: FilesVerified, restores: 1, starts: 1},
		{name: "5-ambiguous-hot-receipt", pending: true, phase: RuntimeStarted, restores: 1, starts: 1},
		{name: "6-unprovable-commit-durability", corrupt: true, want: ErrJournal},
		{name: "6-visible-commit-after-resync", committed: true, starts: 1},
		{name: "7-durable-commit-without-report", committed: true, starts: 1},
		{name: "8-terminal-rollback", pending: true, phase: Stopped, fail: true, want: ErrRollbackFailed},
		{name: "8-corrupt-evidence", corrupt: true, want: ErrJournal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := pendingFixture(t)
			w := &startupWorking{restoreFixture: f}
			journal := startupJournal{Journal: f}
			if tc.corrupt {
				journal.readError = ErrJournal
			}
			if !tc.pending {
				f.record.Pending = nil
			} else {
				f.record.Pending.Phase = tc.phase
				if tc.phase == RuntimeStarted {
					runtime := f.old.Runtime
					runtime.PID, runtime.StartTick = 22, 2
					f.record.Pending.Started = &runtime
				}
				if tc.fail {
					f.record.Pending.Rollback = &RollbackProgress{State: RollbackFailed}
				}
			}
			if tc.committed {
				f.record.Current = &Confirmed{AttemptID: "01010101010101010101010101010101", Number: 1, Tag: f.proposed, Baseline: f.old}
			}
			if err := RecoverStartup(t.Context(), journal, w); !errors.Is(err, tc.want) {
				t.Fatalf("startup = %v, want %v", err, tc.want)
			}
			if f.calls != tc.restores || w.starts != tc.starts {
				t.Fatalf("restored %d, started %d", f.calls, w.starts)
			}
			if tc.committed && (f.record.Current == nil || f.record.Current.Number != 1) {
				t.Fatal("durable result changed")
			}
			if tc.restores == 1 {
				if err := RecoverStartup(t.Context(), f, w); err != nil || f.calls != 1 || f.record.Working() == nil || f.record.Current != nil {
					t.Fatalf("reopened baseline = %v, effects %d", err, f.calls)
				}
			}
		})
	}
}

func TestStartupRecoveryResumesCanceledRollback(t *testing.T) {
	f := pendingFixture(t)
	w := &startupWorking{restoreFixture: f}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.restoreHook = func(context.Context, Intent) { cancel() }
	if err := RecoverStartup(ctx, f, w); !errors.Is(err, context.Canceled) || w.starts != 0 || f.record.Pending.Rollback.State != RollbackActive {
		t.Fatalf("interrupted restore: %v", err)
	}
	f.restoreHook = nil
	if err := RecoverStartup(t.Context(), f, w); err != nil || f.calls != 2 || w.starts != 1 {
		t.Fatalf("resumed restore: %v", err)
	}
	w.proofError = preparation.ErrUnavailable
	if err := RecoverStartup(t.Context(), f, w); !errors.Is(err, preparation.ErrUnavailable) || f.calls != 2 {
		t.Fatalf("fresh verification result: %v", err)
	}
}

func TestStartupReestablishesVisibleCommitDurability(t *testing.T) {
	f := pendingFixture(t)
	w := &startupWorking{restoreFixture: f}
	path := privateTestDirectory(t)
	journal, err := OpenJournal(path, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	record := f.record
	record.Pending = nil
	record.Current = &Confirmed{AttemptID: "01010101010101010101010101010101", Number: f.first.Snapshot.Number, Tag: f.first.Tag, Baseline: f.old}
	// Use exact canonical bytes of the committed desired revision.
	for i := range record.Current.Baseline.Files {
		record.Current.Baseline.Files[i].Content = f.first.Snapshot.Files[i].Content
	}
	sync := journal.syncDir
	journal.syncDir = func() error { return ErrJournal }
	if err := journal.Save(record); !errors.Is(err, ErrJournal) {
		t.Fatalf("injected post-rename sync: %v", err)
	}
	if err := RecoverStartup(t.Context(), journal, w); !errors.Is(err, ErrJournal) || w.starts != 0 {
		t.Fatalf("unproven marker allowed startup: %v", err)
	}
	journal.syncDir = sync
	if err := RecoverStartup(t.Context(), journal, w); err != nil || w.starts != 1 || f.calls != 0 {
		t.Fatalf("resynced marker was not rechecked: %v", err)
	}
}
