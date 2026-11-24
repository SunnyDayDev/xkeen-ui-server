package localvalidation

import (
	"context"
	"reflect"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/restart"
)

// DetachRuntime stops the trusted process without changing a confirmed
// generation or its guard. The caller holds the validator and journal locks.
func (w *RestartWorking) DetachRuntime(ctx context.Context, journal restart.Journal) error {
	record, err := journal.Load()
	if err != nil {
		return err
	}
	if record.Pending != nil {
		if _, err := restart.RestorePending(ctx, journal, w, record.Pending.AttemptID); err != nil {
			return err
		}
		record, err = journal.Load()
		if err != nil {
			return err
		}
	}
	working := record.Working()
	ids, err := w.scopedProcessIDs()
	if err != nil || len(ids) > 1 {
		return restart.ErrBaseline
	}
	if len(ids) == 0 {
		// A repeated detach after a confirmed stop still requires evidence
		// that the held disk generation belongs to this directory.
		if working == nil || !w.matchesReceiptDirectory(working.Baseline.Runtime) ||
			!w.exactFiles(working.Baseline.Files) ||
			(working.Hot != nil && working.Hot.Rebound(w.policy(ctx)) == nil) {
			return restart.ErrBaseline
		}
		return nil
	}
	runtime, err := w.Observe(ctx, working)
	if err != nil || ids[0] != runtime.Runtime.PID {
		return restart.ErrBaseline
	}
	if working == nil {
		record.LocalBaseline = &runtime
		if err := journal.Save(record); err != nil {
			return err
		}
	} else if !reflect.DeepEqual(runtime, working.Baseline) {
		return restart.ErrBaseline
	}
	probe := w.initial
	probe.PID, probe.StartTick = runtime.Runtime.PID, runtime.Runtime.StartTick
	if err := stopExact(ctx, probe); err != nil {
		return err
	}
	if w.scopeHasLiveProcess() || !w.exactFiles(runtime.Files) {
		return restart.ErrBaseline
	}
	return nil
}
