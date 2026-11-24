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
	AttemptID string
	Number    int64
	Tag       string
	Baseline  preparation.Baseline
}

// Intent contains everything needed to resolve interrupted changes without
// network authority or agent-owned cache.
type Intent struct {
	AttemptID string
	Previous  preparation.Baseline
	Target    Desired
}

type Record struct {
	Version int
	Scope   preparation.Scope
	Current *Confirmed
	Pending *Intent
}

type Source interface {
	ReadCurrent(context.Context, string) (Desired, error)
}

type Journal interface {
	Load() (Record, error)
	Save(Record) error
}

type Working interface {
	Observe(context.Context, *Confirmed) (preparation.Baseline, error)
	Preflight(context.Context, preparation.Baseline, Desired) error
	Install(context.Context, Intent) (preparation.Baseline, error)
}

type Service struct {
	scope     preparation.Scope
	source    Source
	journal   Journal
	working   Working
	validator preparation.Validator
	mu        sync.Mutex
}

func New(scope preparation.Scope, source Source, journal Journal, working Working, validator preparation.Validator) *Service {
	return &Service{scope: scope, source: source, journal: journal, working: working, validator: validator}
}

func (s *Service) Apply(ctx context.Context, key, tag string) (Confirmed, error) {
	if !s.mu.TryLock() {
		return Confirmed{}, ErrPending
	}
	defer s.mu.Unlock()
	record, err := s.journal.Load()
	if err != nil || record.Version != Version || record.Scope != s.scope {
		return Confirmed{}, ErrJournal
	}
	if record.Pending != nil {
		return Confirmed{}, ErrPending
	}
	candidate, err := s.read(ctx, key, tag)
	if err != nil {
		return Confirmed{}, err
	}
	before, err := s.working.Observe(ctx, record.Current)
	if err != nil || before.Runtime.VerifierID != s.scope.VerifierID || before.Runtime.PID < 1 || before.Runtime.StartTick == 0 ||
		(record.Current != nil && !reflect.DeepEqual(before.Files, record.Current.Baseline.Files)) {
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
	if err := s.working.Preflight(ctx, before, candidate); err != nil {
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
	intent := Intent{AttemptID: hex.EncodeToString(id[:]), Previous: before, Target: fresh}
	record.Pending = &intent
	if err := s.journal.Save(record); err != nil {
		return Confirmed{}, ErrJournal
	}
	now, err := s.working.Install(ctx, intent)
	if err != nil || ctx.Err() != nil || !validInstallation(intent, now, s.scope.VerifierID) {
		return Confirmed{}, ErrEffects
	}
	confirmed := Confirmed{AttemptID: intent.AttemptID, Number: fresh.Snapshot.Number, Tag: fresh.Tag, Baseline: now}
	record.Current, record.Pending = &confirmed, nil
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
	if actual.Runtime == intent.Previous.Runtime || actual.Runtime.VerifierID != verifier || actual.Runtime.PID < 1 ||
		actual.Runtime.StartTick == 0 || len(actual.Files) != len(intent.Target.Snapshot.Files) {
		return false
	}
	for i, file := range intent.Target.Snapshot.Files {
		if file.Name != actual.Files[i].Name || !bytes.Equal(file.Content, actual.Files[i].Content) {
			return false
		}
	}
	return true
}
