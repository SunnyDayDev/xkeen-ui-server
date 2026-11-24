//go:build linux

package localvalidation

import (
	"context"
	"reflect"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/restart"
)

func (w *RestartWorking) Restore(ctx context.Context, intent restart.Intent, started func(preparation.Runtime) error) (preparation.Baseline, error) {
	before := intent.Previous
	if !w.matchesReceiptDirectory(before.Runtime) || !w.sameDirectory() || ctx.Err() != nil {
		return preparation.Baseline{}, restart.ErrBaseline
	}
	binary, err := w.workingBinary()
	if err != nil {
		return preparation.Baseline{}, err
	}
	binary.Close()
	beforeStop, err := w.rollbackFiles(intent)
	if err != nil {
		return preparation.Baseline{}, err
	}
	if err := w.stopRestorableProcess(ctx, intent); err != nil {
		return preparation.Baseline{}, err
	}
	afterStop, err := w.rollbackFiles(intent)
	if err != nil || !reflect.DeepEqual(beforeStop, afterStop) {
		return preparation.Baseline{}, restart.ErrBaseline
	}
	if err := w.installFiles(ctx, afterStop, before.Files); err != nil {
		return preparation.Baseline{}, err
	}
	if !w.exactFiles(before.Files) || w.scopeHasLiveProcess() {
		return preparation.Baseline{}, restart.ErrBaseline
	}
	return w.launch(ctx, before.Files, started)
}

func (w *RestartWorking) rollbackFiles(intent restart.Intent) ([]preparation.BaselineFile, error) {
	files, _, err := workingFiles(w.initial.Directory)
	if err != nil || !w.sameDirectory() || !recoverableFiles(files, intent.Previous.Files, intent.TargetFiles) {
		return nil, restart.ErrBaseline
	}
	return files, nil
}

func recoverableFiles(actual, prior, target []preparation.BaselineFile) bool {
	before := fileNames(prior)
	after := fileNames(target)
	present := make(map[string]bool, len(actual))
	for _, file := range actual {
		old, inOld := before[file.Name]
		next, inNext := after[file.Name]
		if present[file.Name] || !(inOld && reflect.DeepEqual(file, old) || inNext && reflect.DeepEqual(file, next)) {
			return false
		}
		present[file.Name] = true
	}
	for name := range before {
		if _, both := after[name]; !present[name] && (name != "06_extra.json" || both) {
			return false
		}
	}
	for name := range after {
		if _, both := before[name]; !present[name] && (name != "06_extra.json" || both) {
			return false
		}
	}
	return true
}

func fileNames(files []preparation.BaselineFile) map[string]preparation.BaselineFile {
	names := make(map[string]preparation.BaselineFile, len(files))
	for _, file := range files {
		names[file.Name] = file
	}
	return names
}

func (w *RestartWorking) stopRestorableProcess(ctx context.Context, intent restart.Intent) error {
	authorized := []*preparation.Runtime{&intent.Previous.Runtime, intent.Started}
	if intent.Rollback != nil {
		authorized = append(authorized, intent.Rollback.Started)
	}
	scope, err := w.scopedProcessIDs()
	if err != nil || len(scope) > 1 {
		return restart.ErrBaseline
	}
	var expected *Working
	for _, rt := range authorized {
		if rt == nil || rt.BootID != w.bootID {
			continue
		}
		tick, err := ProcessStartTick(rt.PID)
		if err != nil || tick != rt.StartTick {
			continue
		}
		if len(scope) != 1 || scope[0] != rt.PID || !w.matchesReceiptDirectory(*rt) {
			return restart.ErrBaseline
		}
		probe := w.initial
		probe.PID, probe.StartTick = rt.PID, rt.StartTick
		expected = &probe
	}
	if len(scope) == 1 && expected == nil {
		return restart.ErrBaseline
	}
	if expected != nil {
		if err := stopExact(ctx, *expected); err != nil {
			return err
		}
	}
	if w.scopeHasLiveProcess() || ctx.Err() != nil {
		return restart.ErrBaseline
	}
	return nil
}
