package restart

import (
	"context"
	"errors"
	"reflect"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
)

var (
	ErrRolledBack     = errors.New("revision application failed and the previous runtime was restored")
	ErrRollbackFailed = errors.New("revision rollback failed and current runtime is unknown")
)

type RollbackState string

const (
	RollbackActive RollbackState = "restoring"
	RollbackFailed RollbackState = "rollback_failed"
)

type RollbackProgress struct {
	State   RollbackState
	Started *preparation.Runtime
}

type RollbackResult struct {
	AttemptID     string
	TargetTag     string
	TargetNumber  int64
	FailedPhase   Phase
	RetryApproved bool
}

type Restorer interface {
	Restore(context.Context, Intent, func(preparation.Runtime) error) (preparation.Baseline, error)
}

// RestorePending resolves one attempt from protected durable authority.
// The caller holds the exclusive journal and working-scope locks. No central
// credential, desired cache or agent process is involved in this operation.
func RestorePending(ctx context.Context, journal Journal, working Restorer, expectedID string) (RollbackResult, error) {
	if err := ctx.Err(); err != nil {
		return RollbackResult{}, err
	}
	record, err := journal.Load()
	if err != nil {
		return RollbackResult{}, ErrJournal
	}
	if record.LastRollback != nil && record.LastRollback.AttemptID == expectedID {
		return *record.LastRollback, nil
	}
	if record.Pending == nil || !validAttemptID(expectedID) || record.Pending.AttemptID != expectedID {
		return RollbackResult{}, ErrPending
	}
	pending := *record.Pending
	if pending.Rollback == nil {
		pending.Rollback = &RollbackProgress{State: RollbackActive}
		record.Pending = &pending
		if err := journal.Save(record); err != nil {
			return RollbackResult{}, ErrJournal
		}
	}
	if pending.Rollback.State != RollbackActive {
		return RollbackResult{}, ErrRollbackFailed
	}
	beforeRollback := pending
	restored, err := working.Restore(ctx, beforeRollback, func(runtime preparation.Runtime) error {
		if ctx.Err() != nil || !validRollbackRuntime(record.Scope, pending, runtime) ||
			(pending.Rollback.Started != nil && runtime == *pending.Rollback.Started) {
			return ErrJournal
		}
		next := pending
		next.Rollback = &RollbackProgress{State: RollbackActive, Started: &runtime}
		record.Pending = &next
		if err := journal.Save(record); err != nil {
			return ErrJournal
		}
		pending = next
		return nil
	})
	if errors.Is(err, ErrJournal) {
		// In particular, do not overwrite state that became visible after a
		// failed sync. Recovery must reopen and resolve that evidence.
		return RollbackResult{}, ErrJournal
	}
	if err != nil || ctx.Err() != nil || pending.Rollback.Started == nil ||
		*pending.Rollback.Started != restored.Runtime || !validBaseline(record.Scope, restored) ||
		!reflect.DeepEqual(restored.Files, pending.Previous.Files) {
		next := pending
		next.Rollback = &RollbackProgress{State: RollbackFailed, Started: pending.Rollback.Started}
		record.Pending = &next
		if err := journal.Save(record); err != nil {
			return RollbackResult{}, ErrJournal
		}
		return RollbackResult{}, ErrRollbackFailed
	}
	result := RollbackResult{AttemptID: pending.AttemptID, TargetTag: pending.Target.Tag,
		TargetNumber: pending.Target.Snapshot.Number, FailedPhase: pending.Phase}
	if record.Current == nil {
		record.LocalBaseline = &restored
	} else {
		current := *record.Current
		current.Baseline = restored
		record.Current = &current
	}
	record.Pending, record.LastRollback = nil, &result
	if err := journal.Save(record); err != nil {
		return RollbackResult{}, ErrJournal
	}
	return result, nil
}

// AllowRollbackRetry records a one-use local permit for the most recent
// successful rollback. It performs no working effects and preserves history.
func AllowRollbackRetry(journal Journal, expectedID string) error {
	record, err := journal.Load()
	if err != nil {
		return ErrJournal
	}
	result := record.LastRollback
	if !validAttemptID(expectedID) || record.Pending != nil || result == nil || result.AttemptID != expectedID ||
		(record.Current != nil && record.Current.Number >= result.TargetNumber) {
		return ErrPending
	}
	if result.RetryApproved {
		return nil
	}
	next := *result
	next.RetryApproved = true
	record.LastRollback = &next
	if err := journal.Save(record); err != nil {
		return ErrJournal
	}
	return nil
}

func validOriginalPhase(phase Phase) bool {
	switch phase {
	case Planned, Stopped, FilesVerified, RuntimeStarted:
		return true
	}
	return false
}

func validRollbackRuntime(scope preparation.Scope, pending Intent, runtime preparation.Runtime) bool {
	prior := pending.Previous.Runtime
	return runtime.PID > 0 && runtime.StartTick > 0 && runtime.VerifierID == scope.VerifierID &&
		runtime.DirectoryDevice == prior.DirectoryDevice && runtime.DirectoryInode == prior.DirectoryInode &&
		runtime != prior && (pending.Started == nil || runtime != *pending.Started)
}
