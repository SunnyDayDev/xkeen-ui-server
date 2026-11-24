package restart

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestApplyGuardsIntentAndPromotesExactlyOnce(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*applyFixture)
		want      error
		effects   []string
	}{
		{name: "commits", effects: []string{"intent", "install", "commit"}},
		{name: "changed desired", configure: func(f *applyFixture) { f.second.Snapshot.Number++ }, want: ErrDesired},
		{name: "revoked during validation", configure: func(f *applyFixture) { f.secondErr = ErrDesired }, want: ErrDesired},
		{name: "forged initial tag", configure: func(f *applyFixture) { f.proposed += "forged" }, want: ErrDesired},
		{name: "invalid working set", configure: func(f *applyFixture) { f.observeErr = ErrBaseline }, want: ErrBaseline},
		{name: "failed intent", configure: func(f *applyFixture) { f.failIntent = true }, want: ErrJournal, effects: []string{"intent"}},
		{name: "failed install", configure: func(f *applyFixture) { f.installErr = errors.New("fixture") }, want: ErrEffects, effects: []string{"intent", "install"}},
		{name: "failed commit", configure: func(f *applyFixture) { f.failCommit = true }, want: ErrJournal, effects: []string{"intent", "install", "commit"}},
		{name: "pending", configure: func(f *applyFixture) { f.record.Pending = &Intent{} }, want: ErrPending},
		{name: "replay", configure: func(f *applyFixture) {
			f.record.Current = &Confirmed{Baseline: f.old, AttemptID: "confirmed", Number: f.first.Snapshot.Number, Tag: f.proposed}
		}},
		{name: "replay changed baseline", configure: func(f *applyFixture) {
			f.record.Current = &Confirmed{Baseline: preparation.Baseline{}, AttemptID: "confirmed", Number: f.first.Snapshot.Number, Tag: f.proposed}
		}, want: ErrBaseline},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newApplyFixture(t)
			if tc.configure != nil {
				tc.configure(f)
			}
			out, err := New(f.scope, f, f, f, f).Apply(t.Context(), "ephemeral", f.proposed)
			if !errors.Is(err, tc.want) || !slices.Equal(f.effects, tc.effects) {
				t.Fatalf("apply = %v, effects %v", err, f.effects)
			}
			if tc.name == "commits" && (out.Tag != f.proposed || out.AttemptID == "" || f.record.Pending != nil || f.record.Current.Baseline.Runtime.PID != 22) {
				t.Fatal("commit failed to promote the verified working generation")
			}
			if tc.name == "replay" && out.AttemptID != "confirmed" {
				t.Fatal("replay changed the stable result")
			}
		})
	}
}

type applyFixture struct {
	scope                             preparation.Scope
	record                            Record
	first, second                     Desired
	old                               preparation.Baseline
	proposed                          string
	effects                           []string
	fetches                           int
	secondErr, observeErr, installErr error
	failIntent, failCommit            bool
	installHook                       func()
}

func newApplyFixture(t *testing.T) *applyFixture {
	t.Helper()
	scope := preparation.Scope{Origin: "https://controller.example", RouterID: "router-example", Profile: "26.3.27", VerifierID: "fixture"}
	snapshot := agentrevision.Snapshot{Number: 1, Profile: scope.Profile}
	for _, name := range []string{"01_log.json", "02_api.json", "03_inbounds.json", "04_outbounds.json", "05_routing.json"} {
		snapshot.Files = append(snapshot.Files, configuration.CatalogFile{Name: name, Content: []byte("{}")})
	}
	tag, err := agentrevision.Tag(scope.RouterID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	first := Desired{Snapshot: snapshot, Tag: tag}
	old := preparation.Baseline{Runtime: preparation.Runtime{PID: 11, StartTick: 1, VerifierID: scope.VerifierID}}
	for _, file := range snapshot.Files {
		old.Files = append(old.Files, preparation.BaselineFile{Name: file.Name, Content: file.Content, Mode: 0600})
	}
	return &applyFixture{scope: scope, record: Record{Version: Version, Scope: scope}, first: first, second: first, old: old, proposed: tag}
}

func (f *applyFixture) ReadCurrent(context.Context, string) (Desired, error) {
	f.fetches++
	if f.fetches == 1 {
		return f.first, nil
	}
	return f.second, f.secondErr
}

func (f *applyFixture) Load() (Record, error) { return f.record, nil }

func (f *applyFixture) Save(record Record) error {
	if record.Pending != nil {
		if record.Pending.Phase == Planned {
			f.effects = append(f.effects, "intent")
		}
		if f.failIntent {
			return ErrJournal
		}
	} else {
		f.effects = append(f.effects, "commit")
		if f.failCommit {
			return ErrJournal
		}
	}
	f.record = record
	return nil
}

func (f *applyFixture) Observe(context.Context, *Confirmed) (preparation.Baseline, error) {
	return f.old, f.observeErr
}

func (f *applyFixture) Preflight(context.Context, preparation.Baseline, Desired) ([]preparation.BaselineFile, error) {
	return f.old.Files, nil
}

func (f *applyFixture) Install(_ context.Context, intent Intent, advance func(Phase, *preparation.Runtime) error) (preparation.Baseline, error) {
	f.effects = append(f.effects, "install")
	if f.installHook != nil {
		f.installHook()
	}
	current := intent.Previous
	current.Runtime.PID, current.Runtime.StartTick = 22, 2
	if f.installErr != nil {
		return preparation.Baseline{}, f.installErr
	}
	if err := advance(Stopped, nil); err != nil {
		return preparation.Baseline{}, err
	}
	if err := advance(FilesVerified, nil); err != nil {
		return preparation.Baseline{}, err
	}
	return current, advance(RuntimeStarted, &current.Runtime)
}

func (f *applyFixture) Check(context.Context, preparation.Scope, agentrevision.Snapshot) error {
	return nil
}

func TestCancellationBeforeAndAfterIntent(t *testing.T) {
	t.Run("before", func(t *testing.T) {
		f := newApplyFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		svc := New(f.scope, f, f, f, centralValidationHook(cancel))
		_, err := svc.Apply(ctx, "synthetic", f.proposed)
		if !errors.Is(err, context.Canceled) || len(f.effects) != 0 || f.record.Pending != nil {
			t.Fatal("cancellation before intent had effects", err)
		}
	})
	t.Run("after", func(t *testing.T) {
		f := newApplyFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		f.installHook = cancel
		f.installErr = context.Canceled
		svc := New(f.scope, f, f, f, f)
		_, err := svc.Apply(ctx, "synthetic", f.proposed)
		if err != ErrEffects || !slices.Equal(f.effects, []string{"intent", "install"}) ||
			f.record.Pending == nil || f.record.Current != nil {
			t.Fatal("cancellation after intent did not retain the evidence", err)
		}
	})
}
