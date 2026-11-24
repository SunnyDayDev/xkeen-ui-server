package preparation

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/desired"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func fixture(t *testing.T) (Scope, *agentrevision.Snapshot, *desired.Entry, Baseline) {
	t.Helper()
	scope := Scope{Origin: "https://server.example", RouterID: "one.example", Profile: configuration.XrayIdentityVersion, VerifierID: "pinned-profile"}
	candidate := &agentrevision.Snapshot{Number: 8, Profile: scope.Profile}
	for _, name := range []string{"01_log.json", "02_api.json", "03_inbounds.json", "04_outbounds.json", "05_routing.json"} {
		candidate.Files = append(candidate.Files, configuration.CatalogFile{Name: name, Content: []byte("{}")})
	}
	base := Baseline{Runtime: Runtime{PID: 10, StartTick: 30, VerifierID: scope.VerifierID},
		Files: []BaselineFile{{Name: "01_log.json", Content: []byte(" {}\n"), UID: 7, GID: 8, Mode: 0640}}}
	tag, body, err := agentrevision.Encode(scope.RouterID, *candidate)
	if err != nil {
		t.Fatal(err)
	}
	return scope, candidate, &desired.Entry{Origin: scope.Origin, RouterID: scope.RouterID, Tag: tag, Body: body}, base
}

type observerStub struct {
	base         Baseline
	err          error
	changeOnRead bool
	reads        int
}

func (s *observerStub) Observe(context.Context) (Baseline, error) {
	s.reads++
	if s.changeOnRead && s.reads > 1 {
		s.base.Runtime.StartTick++
	}
	return s.base, s.err
}

type repoStub struct {
	previousID string
	writes     int
	pkg        *Package
}

func (s *repoStub) BaselineID() (string, error) { return s.previousID, nil }
func (s *repoStub) SavePrepared(p Package) error {
	s.writes++
	s.pkg = &p
	return nil
}

type validationFunc func(context.Context, Scope, agentrevision.Snapshot) error

func (f validationFunc) Check(ctx context.Context, scope Scope, candidate agentrevision.Snapshot) error {
	return f(ctx, scope, candidate)
}

func TestPreparationBindsFullCandidateAndInitialBaseline(t *testing.T) {
	scope, snapshot, entry, base := fixture(t)
	repo := &repoStub{}
	observer := &observerStub{base: base}
	service := New(observer, repo, validationFunc(func(_ context.Context, got Scope, exact agentrevision.Snapshot) error {
		if got != scope || !reflect.DeepEqual(exact, *snapshot) {
			t.Fatal("validation did not receive complete scope and original bytes")
		}
		return nil
	}))
	got, err := service.Prepare(t.Context(), scope, entry)
	if err != nil || got == nil || got.BaselineID == "" || got.CandidateTag == "" || repo.writes != 1 || observer.reads != 2 {
		t.Fatalf("prepared artifact=%v err=%v writes=%d", got != nil, err, repo.writes)
	}
	snapshot.Files[0].Content[0] = 'x'
	base.Files[0].Content[0] = 'x'
	if string(repo.pkg.Candidate.Files[0].Content) != "{}" || string(repo.pkg.Baseline.Files[0].Content) != " {}\n" ||
		repo.pkg.Baseline.Files[0].Mode != 0640 || repo.pkg.Baseline.Files[0].UID != 7 {
		t.Fatal("prepared material changed with a source buffer")
	}
}

func TestPreparationFailsClosedBeforeReadyWrite(t *testing.T) {
	for name, problem := range map[string]struct {
		stale, changed, corrupt, canceled, absent bool
	}{
		"wrong baseline": {stale: true}, "working runtime changed": {changed: true},
		"validator mutated input": {corrupt: true}, "late result after cancel": {canceled: true}, "missing desired": {absent: true},
	} {
		t.Run(name, func(t *testing.T) {
			scope, _, entry, base := fixture(t)
			repo := &repoStub{}
			observer := &observerStub{base: base, changeOnRead: problem.changed}
			if problem.stale {
				repo.previousID = "different-baseline"
			}
			if problem.absent {
				entry = nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			service := New(observer, repo, validationFunc(func(_ context.Context, _ Scope, candidate agentrevision.Snapshot) error {
				if problem.corrupt {
					candidate.Files[0].Content[0] = 'x'
				}
				if problem.canceled {
					cancel()
				}
				return nil
			}))
			got, err := service.Prepare(ctx, scope, entry)
			if problem.absent {
				if got != nil || err != nil || observer.reads != 0 {
					t.Fatal("no desired started preparation")
				}
			} else if err == nil || got != nil {
				t.Fatal("unsafe preparation became ready")
			}
			if problem.canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel = %v", err)
			}
			if repo.writes != 0 {
				t.Fatal("failed preparation wrote a ready package")
			}
		})
	}
}

func TestPreparationRejectsAProofFromAnotherRouterOrOrigin(t *testing.T) {
	for name, modify := range map[string]func(*Scope){
		"router": func(s *Scope) { s.RouterID = "another.example" },
		"origin": func(s *Scope) { s.Origin = "https://another.example" },
	} {
		t.Run(name, func(t *testing.T) {
			scope, _, entry, base := fixture(t)
			modify(&scope)
			repo := &repoStub{}
			observer := &observerStub{base: base}
			service := New(observer, repo, validationFunc(func(context.Context, Scope, agentrevision.Snapshot) error {
				t.Fatal("foreign proof reached validator")
				return nil
			}))
			if got, err := service.Prepare(t.Context(), scope, entry); got != nil || !errors.Is(err, ErrCandidate) || observer.reads != 0 || repo.writes != 0 {
				t.Fatalf("foreign proof was accepted: %v", err)
			}
		})
	}
}
