// Package restart coordinates preparation, durable intent and controlled restart.
package restart

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

const Version = 1

var (
	ErrDesired  = errors.New("current desired revision could not be confirmed")
	ErrBaseline = errors.New("working revision could not be confirmed")
	ErrPending  = errors.New("an unresolved revision application blocks the working scope")
	ErrJournal  = errors.New("revision application journal is unavailable")
	ErrEffects  = errors.New("revision restart did not reach a verified working state")
)

type Desired struct {
	Tag      string
	Snapshot agentrevision.Snapshot
}

type Confirmed struct {
	Guard     string      `json:",omitempty"`
	Hot       *HotReceipt `json:",omitempty"`
	AttemptID string
	Number    int64
	Tag       string
	Baseline  preparation.Baseline
}

// Intent contains everything needed to resolve interrupted changes without
// network authority or agent-owned cache.
type Phase string

const (
	Planned        Phase = "planned"
	Stopped        Phase = "stopped"
	FilesVerified  Phase = "files_verified"
	RuntimeStarted Phase = "runtime_started"
)

type Intent struct {
	Hot         *HotReceipt `json:",omitempty"`
	AttemptID   string
	Previous    preparation.Baseline
	Target      Desired
	TargetFiles []preparation.BaselineFile
	Phase       Phase
	Started     *preparation.Runtime
	Rollback    *RollbackProgress
}

type Record struct {
	Version       int
	Scope         preparation.Scope
	Current       *Confirmed
	LocalBaseline *preparation.Baseline
	LastRollback  *RollbackResult
	Pending       *Intent
	Delivery      *ReportDelivery `json:",omitempty"`
}

// Working returns the recorded baseline authority, including a restored local
// initial generation. A pending attempt may make this history diverge from the
// actual runtime. Callers must honor the pending barrier and reverify.
func (r Record) Working() *Confirmed {
	if r.Current != nil {
		return r.Current
	}
	if r.LocalBaseline != nil {
		return &Confirmed{Baseline: *r.LocalBaseline}
	}
	return nil
}

type Source interface {
	ReadCurrent(context.Context, string) (Desired, error)
}

type Journal interface {
	Load() (Record, error)
	Save(Record) error
}

type Working interface {
	Restorer
	Observe(context.Context, *Confirmed) (preparation.Baseline, error)
	Preflight(context.Context, preparation.Baseline, Desired) ([]preparation.BaselineFile, error)
	Install(context.Context, Intent, func(Phase, *preparation.Runtime) error) (preparation.Baseline, error)
}

type Service struct {
	scope     preparation.Scope
	source    Source
	journal   Journal
	working   Working
	validator preparation.Validator
	mu        sync.Mutex
	applying  sync.Mutex
}

func New(scope preparation.Scope, source Source, journal Journal, working Working, validator preparation.Validator) *Service {
	return &Service{scope: scope, source: source, journal: journal, working: working, validator: validator}
}

func (s *Service) Apply(ctx context.Context, key, tag string) (Confirmed, error) {
	if !s.applying.TryLock() {
		return Confirmed{}, ErrPending
	}
	defer s.applying.Unlock()
	// One application may wait for an in-flight report. Other application
	// requests cannot queue their own mutations behind the scope lock.
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Confirmed{}, err
	}
	record, err := s.journal.Load()
	if err != nil || record.Version != Version || record.Scope != s.scope {
		return Confirmed{}, ErrJournal
	}
	if record.Pending != nil {
		if record.Pending.Rollback != nil && record.Pending.Rollback.State == RollbackFailed {
			return Confirmed{}, ErrRollbackFailed
		}
		return Confirmed{}, ErrPending
	}
	if record.Delivery != nil && record.Delivery.Outcome != nil && !record.Delivery.Done {
		return Confirmed{}, ErrReportPending
	}
	candidate, err := s.read(ctx, key, tag)
	if err != nil {
		return Confirmed{}, err
	}
	if last := record.LastRollback; last != nil && last.TargetTag == tag && !last.RetryApproved {
		return Confirmed{}, ErrRolledBack
	}
	working := record.Working()
	before, err := s.working.Observe(ctx, working)
	if err != nil || before.Runtime.VerifierID != s.scope.VerifierID || before.Runtime.PID < 1 || before.Runtime.StartTick == 0 ||
		(working != nil && !reflect.DeepEqual(before.Files, working.Baseline.Files)) {
		return Confirmed{}, ErrBaseline
	}
	if record.Current != nil {
		if candidate.Tag == record.Current.Tag {
			return *record.Current, nil
		}
		if candidate.Snapshot.Number <= record.Current.Number {
			return Confirmed{}, ErrDesired
		}
	}
	files, err := s.working.Preflight(ctx, before, candidate)
	if err != nil {
		return Confirmed{}, err
	}
	if err := s.validator.Check(ctx, s.scope, candidate.Snapshot); err != nil {
		return Confirmed{}, err
	}
	fresh, err := s.read(ctx, key, tag)
	if err != nil || !reflect.DeepEqual(candidate, fresh) {
		return Confirmed{}, ErrDesired
	}
	if err := ctx.Err(); err != nil {
		return Confirmed{}, err
	}
	var id [16]byte
	rand.Read(id[:])
	intent := Intent{AttemptID: hex.EncodeToString(id[:]), Previous: before, Target: fresh, TargetFiles: files, Phase: Planned}
	// The restart-only adapter has no exclusive RoutingService writer.
	// E07 supplies fresh rule evidence when that capability is installed.
	if last := record.LastRollback; last != nil && last.RetryApproved {
		next := *last
		next.RetryApproved = false
		record.LastRollback = &next
	}
	var evidence RuleEvidence
	hot, canHot := s.working.(hotWorking)
	if canHot && record.Current != nil && record.Current.Guard != "" {
		evidence = hot.RuleEvidence(ctx, record.Current)
	}
	plan := intent.Plan(record.Current, evidence)
	if plan.Rules != nil {
		check, err := s.read(ctx, key, tag)
		if err != nil || !reflect.DeepEqual(check, fresh) || ctx.Err() != nil {
			return Confirmed{}, ErrDesired
		}
		intent.Hot = &HotReceipt{AllowPort: evidence.AllowPort, DenyPort: evidence.DenyPort, ProbePort: evidence.ProbePort, Guard: record.Current.Guard, Expected: plan.Rules.Expected, Outcome: plan.Rules.Outcome}
		intent.Phase = HotDispatch
	}
	if plan.Unchanged {
		again, err := s.working.Observe(ctx, record.Current)
		if err != nil || !reflect.DeepEqual(before, again) || ctx.Err() != nil {
			return Confirmed{}, ErrBaseline
		}
		confirmed := Confirmed{AttemptID: intent.AttemptID, Number: fresh.Snapshot.Number, Tag: fresh.Tag, Baseline: again, Guard: record.Current.Guard, Hot: record.Current.Hot}
		record.Current = &confirmed
		if err := queueOutcome(&record, appliedOutcome(confirmed)); err != nil {
			return Confirmed{}, err
		}
		if err := s.journal.Save(record); err != nil {
			return Confirmed{}, ErrJournal
		}
		return confirmed, nil
	}
	record.Pending = &intent
	if err := s.journal.Save(record); err != nil {
		return Confirmed{}, ErrJournal
	}
	install := s.working.Install
	if intent.Hot != nil {
		install = hot.InstallHot
	}
	now, err := install(ctx, intent, func(phase Phase, runtime *preparation.Runtime) error {
		switch {
		case intent.Phase == HotDispatch && phase == HotVerified && runtime == nil:
		case intent.Phase == HotVerified && phase == HotFiles && runtime != nil && *runtime == before.Runtime:
		case intent.Phase == Planned && phase == Stopped && runtime == nil:
		case intent.Phase == Stopped && phase == FilesVerified && runtime == nil:
		case intent.Phase == FilesVerified && phase == RuntimeStarted && runtime != nil:
		default:
			return ErrJournal
		}
		next := intent
		next.Phase, next.Started = phase, runtime
		record.Pending = &next
		if err := s.journal.Save(record); err != nil {
			return err
		}
		intent = next
		return nil
	})
	if err != nil || ctx.Err() != nil || (intent.Phase != RuntimeStarted && intent.Phase != HotFiles) || intent.Started == nil ||
		*intent.Started != now.Runtime || !validInstallation(intent, now, s.scope.VerifierID) {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 25*time.Second)
		defer cancel()
		if _, err := RestorePending(rollbackCtx, s.journal, s.working, intent.AttemptID); err != nil {
			return Confirmed{}, err
		}
		return Confirmed{}, ErrRolledBack
	}
	confirmed := Confirmed{AttemptID: intent.AttemptID, Number: fresh.Snapshot.Number, Tag: fresh.Tag, Baseline: now, Guard: launchGuard(s.working, now.Runtime), Hot: intent.Hot}
	if intent.Hot != nil {
		confirmed.Guard = intent.Hot.Guard
	}
	record.Current, record.Pending, record.LocalBaseline = &confirmed, nil, nil
	if err := queueOutcome(&record, appliedOutcome(confirmed)); err != nil {
		return Confirmed{}, err
	}
	if err := s.journal.Save(record); err != nil {
		return Confirmed{}, ErrJournal
	}
	return confirmed, nil
}

func (s *Service) read(ctx context.Context, key, tag string) (Desired, error) {
	candidate, err := s.source.ReadCurrent(ctx, key)
	if err != nil || tag == "" || candidate.Tag != tag || candidate.Snapshot.Profile != s.scope.Profile {
		return Desired{}, ErrDesired
	}
	actual, err := agentrevision.Tag(s.scope.RouterID, candidate.Snapshot)
	if err != nil || actual != tag {
		return Desired{}, ErrDesired
	}
	return candidate, nil
}

func validInstallation(intent Intent, actual preparation.Baseline, verifier string) bool {
	if (actual.Runtime == intent.Previous.Runtime) != (intent.Hot != nil) || actual.Runtime.VerifierID != verifier || actual.Runtime.PID < 1 ||
		actual.Runtime.StartTick == 0 || !reflect.DeepEqual(actual.Files, intent.TargetFiles) {
		return false
	}
	for i, file := range intent.Target.Snapshot.Files {
		if file.Name != actual.Files[i].Name || !bytes.Equal(file.Content, actual.Files[i].Content) {
			return false
		}
	}
	return true
}
