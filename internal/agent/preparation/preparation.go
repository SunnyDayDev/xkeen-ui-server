// Package preparation prepares immutable application inputs without changing live state.
package preparation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/desired"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

var (
	ErrBaseline    = errors.New("working baseline is unavailable or changed")
	ErrCandidate   = errors.New("candidate scope or contents are invalid")
	ErrRejected    = errors.New("candidate configuration was rejected")
	ErrUnavailable = errors.New("local validation is unavailable")
	ErrState       = errors.New("prepared state cannot be saved")
)

type Scope struct {
	Origin, RouterID, Profile, VerifierID string
}

type Runtime struct {
	PID        int
	StartTick  uint64
	VerifierID string
}

type BaselineFile struct {
	Name           string
	Content        []byte
	UID, GID, Mode uint32
}

type Baseline struct {
	Runtime Runtime
	Files   []BaselineFile
}

type Package struct {
	Scope        Scope
	BaselineID   string
	Baseline     Baseline
	CandidateTag string
	Candidate    agentrevision.Snapshot
}

// Observer attests the explicitly managed working set and read-only runtime probes.
type Observer interface {
	Observe(context.Context) (Baseline, error)
}

type Repository interface {
	BaselineID() (string, error)
	SavePrepared(Package) error
}

type Validator interface {
	Check(context.Context, Scope, agentrevision.Snapshot) error
}

type Service struct {
	observer   Observer
	repository Repository
	validator  Validator
	mu         sync.Mutex
}

func New(observer Observer, repository Repository, validator Validator) *Service {
	return &Service{observer: observer, repository: repository, validator: validator}
}

func (s *Service) Prepare(ctx context.Context, scope Scope, proof *desired.Entry) (*Package, error) {
	if proof == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if scope.Origin == "" || scope.RouterID == "" || scope.VerifierID == "" ||
		proof.Origin != scope.Origin || proof.RouterID != scope.RouterID {
		return nil, ErrCandidate
	}
	candidate, err := agentrevision.Decode(scope.RouterID, proof.Tag, proof.Body)
	if err != nil || scope.Profile != candidate.Profile {
		return nil, ErrCandidate
	}
	tag := proof.Tag
	before, baselineID, err := s.baseline(ctx, scope)
	if err != nil {
		return nil, err
	}
	previous, err := s.repository.BaselineID()
	if err != nil {
		return nil, ErrState
	}
	if previous != "" && previous != baselineID {
		return nil, ErrBaseline
	}
	checked := copyCandidate(*candidate)
	err = s.validator.Check(ctx, scope, checked)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	checkedTag, digestErr := agentrevision.Tag(scope.RouterID, checked)
	switch {
	case digestErr != nil || checkedTag != tag:
		return nil, ErrUnavailable
	case errors.Is(err, ErrRejected):
		return nil, ErrRejected
	case err != nil:
		return nil, ErrUnavailable
	}
	after, afterID, err := s.baseline(ctx, scope)
	if err != nil || baselineID != afterID || before.Runtime != after.Runtime {
		return nil, ErrBaseline
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := Package{Scope: scope, Baseline: before, BaselineID: baselineID, Candidate: *candidate, CandidateTag: tag}
	if err := s.repository.SavePrepared(result); err != nil {
		return nil, ErrState
	}
	return &result, nil
}

func (s *Service) baseline(ctx context.Context, scope Scope) (Baseline, string, error) {
	base, err := s.observer.Observe(ctx)
	if err != nil {
		return Baseline{}, "", ErrBaseline
	}
	return bindBaseline(scope, base)
}

func bindBaseline(scope Scope, base Baseline) (Baseline, string, error) {
	if base.Runtime.PID < 1 || base.Runtime.StartTick == 0 || base.Runtime.VerifierID != scope.VerifierID ||
		len(base.Files) == 0 || len(base.Files) > 6 {
		return Baseline{}, "", ErrBaseline
	}
	base.Files = slices.Clone(base.Files)
	slices.SortFunc(base.Files, func(a, b BaselineFile) int { return strings.Compare(a.Name, b.Name) })
	remaining := agentrevision.MaxCatalog
	for i, file := range base.Files {
		if !isManagedFile(file.Name) || (i > 0 && base.Files[i-1].Name == file.Name) ||
			len(file.Content) == 0 || len(file.Content) > remaining || file.Mode > 07777 {
			return Baseline{}, "", ErrBaseline
		}
		base.Files[i].Content = bytes.Clone(file.Content)
		remaining -= len(file.Content)
	}
	data, err := json.Marshal(struct {
		Scope Scope
		Files []BaselineFile
	}{scope, base.Files})
	if err != nil {
		return Baseline{}, "", ErrBaseline
	}
	id := sha256.Sum256(data)
	return base, hex.EncodeToString(id[:]), nil
}

func isManagedFile(name string) bool {
	switch name {
	case "01_log.json", "02_api.json", "03_inbounds.json", "04_outbounds.json", "05_routing.json", "06_extra.json":
		return true
	}
	return false
}

func copyCandidate(input agentrevision.Snapshot) agentrevision.Snapshot {
	input.Files = slices.Clone(input.Files)
	for i := range input.Files {
		input.Files[i].Content = bytes.Clone(input.Files[i].Content)
	}
	return input
}

// Valid checks a prepared package restored from private storage.
func (p Package) Valid() bool {
	if p.Scope.Origin == "" || p.Scope.RouterID == "" || p.Scope.VerifierID == "" || p.Scope.Profile != p.Candidate.Profile {
		return false
	}
	tag, err := agentrevision.Tag(p.Scope.RouterID, p.Candidate)
	if err != nil || tag != p.CandidateTag {
		return false
	}
	_, id, err := bindBaseline(p.Scope, p.Baseline)
	return err == nil && id == p.BaselineID
}

// BaselineIdentity validates the working evidence and returns its scoped file identity.
func BaselineIdentity(scope Scope, baseline Baseline) (string, error) {
	_, id, err := bindBaseline(scope, baseline)
	return id, err
}
