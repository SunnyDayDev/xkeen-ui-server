package restart

import (
	"context"
	"errors"
	"math"
	"reflect"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

var ErrReportPending = errors.New("application report is awaiting confirmation")
var ErrReportAuthority = errors.New("application report authority could not be confirmed")

type ReportOutcome struct {
	AttemptID string
	Revision  int64
	Stage     string
	Code      string
}

// ReportDelivery and the terminal application result share one journal write.
type ReportDelivery struct {
	Sequence int64
	Outcome  *ReportOutcome
	Body     *reporting.Report
	Done     bool
}

type ReportRemote interface {
	Identity(context.Context, string) (string, error)
	Cursor(context.Context, string) (int64, error)
	Contact(context.Context, string, *reporting.Report) error
}

func EnableReports(journal Journal) error {
	record, err := journal.Load()
	if err != nil {
		return ErrJournal
	}
	if record.Delivery != nil {
		return nil
	}
	record.Delivery = &ReportDelivery{}
	switch {
	case terminalRollback(record):
		err = queueOutcome(&record, rollbackFailedOutcome(*record.Pending))
	case record.Pending != nil:
		// Startup recovery will checkpoint the outcome under the same lease.
	case record.LastRollback != nil && (record.Current == nil || record.LastRollback.TargetNumber > record.Current.Number):
		err = queueOutcome(&record, rollbackOutcome(*record.LastRollback))
	case record.Current != nil:
		err = queueOutcome(&record, appliedOutcome(*record.Current))
	}
	if err != nil {
		return err
	}
	return journal.Save(record)
}

// TerminalRollbackReport permits reporting only a proved, recorded terminal
// failure. The caller retains the startup gate and both scope leases.
func TerminalRollbackReport(journal Journal) bool {
	record, err := journal.Load()
	return err == nil && terminalRollback(record) && record.Delivery != nil &&
		record.Delivery.Outcome != nil && *record.Delivery.Outcome == rollbackFailedOutcome(*record.Pending)
}

func terminalRollback(record Record) bool {
	return record.Pending != nil && record.Pending.Rollback != nil && record.Pending.Rollback.State == RollbackFailed
}

func queueOutcome(record *Record, outcome ReportOutcome) error {
	if record.Delivery == nil {
		return nil
	}
	if record.Delivery.Outcome != nil && !record.Delivery.Done {
		return ErrReportPending
	}
	record.Delivery = &ReportDelivery{Sequence: record.Delivery.Sequence, Outcome: &outcome}
	return nil
}

func appliedOutcome(current Confirmed) ReportOutcome {
	return ReportOutcome{AttemptID: current.AttemptID, Revision: current.Number}
}

func rollbackOutcome(result RollbackResult) ReportOutcome {
	out := ReportOutcome{AttemptID: result.AttemptID, Revision: result.TargetNumber, Stage: "apply", Code: "apply_failed"}
	switch result.FailedPhase {
	case Stopped:
		out.Stage, out.Code = "write", "write_failed"
	case FilesVerified:
		out.Stage, out.Code = "reload", "reload_failed"
	case RuntimeStarted, HotVerified, HotFiles:
		out.Stage, out.Code = "verify", "runtime_check_failed"
	}
	return out
}

func rollbackFailedOutcome(intent Intent) ReportOutcome {
	return ReportOutcome{AttemptID: intent.AttemptID, Revision: intent.Target.Snapshot.Number,
		Stage: "rollback", Code: "rollback_failed"}
}

func (out ReportOutcome) body(seq int64) reporting.Report {
	result := reporting.Report{Sequence: seq, Revision: out.Revision, State: "failed", Stage: out.Stage, Code: out.Code}
	if out.Stage == "" && out.Code == "" {
		result.State, result.AppliedRevision = "applied", out.Revision
	}
	return result
}

// ReconcileReport never re-applies a revision. The same scope mutex orders
// reporting and runtime changes; heartbeat does not use this mutex.
func (s *Service) ReconcileReport(ctx context.Context, remote ReportRemote, key string, reportOnly bool) (bool, error) {
	if !s.mu.TryLock() {
		return false, ErrReportPending
	}
	defer s.mu.Unlock()
	record, err := s.journal.Load()
	if err != nil || record.Scope != s.scope || record.Version != Version || record.Delivery == nil {
		return false, ErrJournal
	}
	delivery := *record.Delivery
	if reportOnly && (!terminalRollback(record) || delivery.Outcome == nil || *delivery.Outcome != rollbackFailedOutcome(*record.Pending)) {
		return false, ErrReportAuthority
	}
	if delivery.Outcome == nil || (delivery.Done && (delivery.Outcome.Code != "" || record.Pending != nil)) {
		return false, nil
	}
	router, err := remote.Identity(ctx, key)
	if err != nil || router != s.scope.RouterID {
		return false, ErrReportAuthority
	}
	if delivery.Outcome.Code == "" {
		if s.provesCurrent(ctx, record, *delivery.Outcome) {
			if delivery.Done {
				return false, nil
			}
		} else {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			if err := s.retireUnproven(ctx, remote, key, &record, &delivery); err != nil {
				return false, err
			}
		}
	}
	for delivery.Body == nil {
		floor, err := remote.Cursor(ctx, key)
		if err != nil {
			return false, err
		}
		if floor < 0 || max(delivery.Sequence, floor) == math.MaxInt64 {
			return false, ErrJournal
		}
		delivery.Sequence = max(delivery.Sequence, floor) + 1
		body := delivery.Outcome.body(delivery.Sequence)
		if !body.Valid() {
			return false, ErrJournal
		}
		delivery.Body = &body
		reserved := delivery
		record.Delivery = &reserved
		if err := s.journal.Save(record); err != nil {
			return false, err
		}
		// Network cursor reads and journal sync may outlast the first proof.
		if delivery.Outcome.Code == "" && !s.provesCurrent(ctx, record, *delivery.Outcome) {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			if err := s.retireUnproven(ctx, remote, key, &record, &delivery); err != nil {
				return false, err
			}
		}
	}
	if err := remote.Contact(ctx, key, delivery.Body); err != nil {
		return false, err
	}
	delivery.Done = true
	record.Delivery = &delivery
	if err := s.journal.Save(record); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) provesCurrent(ctx context.Context, record Record, outcome ReportOutcome) bool {
	current := record.Current
	if record.Pending != nil || current == nil || current.AttemptID != outcome.AttemptID || current.Number != outcome.Revision {
		return false
	}
	observed, err := s.working.Observe(ctx, current)
	return err == nil && reflect.DeepEqual(observed, current.Baseline)
}

func (s *Service) retireUnproven(ctx context.Context, remote ReportRemote, key string, record *Record, delivery *ReportDelivery) error {
	if delivery.Body != nil && !delivery.Done {
		floor, err := remote.Cursor(ctx, key)
		if err != nil {
			return err
		}
		if floor < 0 {
			return ErrJournal
		}
		if floor >= delivery.Sequence {
			// This cursor can only consume a duplicate or conflict. Never
			// send stale success at a sequence the server hasn't reached.
			if err := remote.Contact(ctx, key, delivery.Body); err != nil {
				return err
			}
		}
	}
	outcome := *delivery.Outcome
	outcome.Stage, outcome.Code = "verify", "runtime_unknown"
	*delivery = ReportDelivery{Sequence: delivery.Sequence, Outcome: &outcome}
	retired := *delivery
	record.Delivery = &retired
	return s.journal.Save(*record)
}

func validDelivery(record Record) bool {
	d := record.Delivery
	if d == nil {
		return true
	}
	if d.Sequence < 0 {
		return false
	}
	if d.Outcome == nil {
		return d.Sequence == 0 && d.Body == nil && !d.Done
	}
	out := *d.Outcome
	if !validAttemptID(out.AttemptID) || !out.body(1).Valid() {
		return false
	}
	switch out.Code {
	case "", "runtime_unknown":
		if record.Current == nil || out.AttemptID != record.Current.AttemptID || out.Revision != record.Current.Number {
			return false
		}
	case "rollback_failed":
		if !terminalRollback(record) || out != rollbackFailedOutcome(*record.Pending) {
			return false
		}
	default:
		if record.LastRollback == nil || out != rollbackOutcome(*record.LastRollback) {
			return false
		}
	}
	if d.Body == nil {
		return !d.Done
	}
	return d.Body.Valid() && *d.Body == out.body(d.Sequence)
}
