package sqlite

import (
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestWebTemplateListReadsCurrentSessionAndExactMetadata(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sessions := NewWebAdmin(db)
	store := NewWebTemplates(db)
	now := time.Unix(1000000000, 0)
	token := [32]byte{1}
	verifier := webadmin.Verifier{Version: 1}
	if err := sessions.SaveVerifier(ctx, verifier, false); err != nil {
		t.Fatal(err)
	}
	if err := sessions.Issue(ctx, 1, token, [32]byte{}, now); err != nil {
		t.Fatal(err)
	}
	empty, err := store.List(ctx, token, fixedWebTime(now))
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty metadata: %#v %v", empty, err)
	}

	for _, row := range []webadmin.TemplateSummary{{ID: "Москва", Version: 1}, {ID: " a ", Version: math.MaxInt64}, {ID: "Branch / #", Version: 2}} {
		if _, err := db.ExecContext(ctx, "INSERT INTO templates(id,draft,version) VALUES(?,?,?)", row.ID, []byte("deliberately unread by listing"), row.Version); err != nil {
			t.Fatal(err)
		}
	}
	want := []webadmin.TemplateSummary{{ID: " a ", Version: math.MaxInt64}, {ID: "Branch / #", Version: 2}, {ID: "Москва", Version: 1}}
	got, err := store.List(ctx, token, fixedWebTime(now.Add(29*time.Minute)))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("metadata: %#v %v", got, err)
	}
	if got, err := store.List(ctx, token, fixedWebTime(now.Add(webadmin.IdleLifetime))); got != nil || !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("list extended idle: %#v %v", got, err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO templates(id,draft) VALUES(CAST(x'ff' AS TEXT),x'5b5d')"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.List(ctx, token, fixedWebTime(now)); got != nil || err == nil {
		t.Fatalf("invalid UTF-8 made the exact list impossible: %#v %v", got, err)
	}
	if err := sessions.SaveVerifier(ctx, verifier, true); err != nil {
		t.Fatal(err)
	}
	if got, err := store.List(ctx, token, fixedWebTime(now)); got != nil || !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("list disclosed metadata after password reset: %#v %v", got, err)
	}
}

func fixtureWebTemplateWriter(t *testing.T) (*sql.DB, WebTemplates, [32]byte, time.Time) {
	t.Helper()
	ctx := t.Context()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sessions := NewWebAdmin(db)
	if err := sessions.SaveVerifier(ctx, webadmin.Verifier{Version: 1}, false); err != nil {
		t.Fatal(err)
	}
	digest, now := [32]byte{5}, time.Unix(1000000000, 0)
	if err := sessions.Issue(ctx, 1, digest, [32]byte{}, now); err != nil {
		t.Fatal(err)
	}
	return db, NewWebTemplates(db), digest, now
}

func TestWebTemplateCreateConflictsAndTouchesOnlyOnCommit(t *testing.T) {
	db, writer, digest, issued := fixtureWebTemplateWriter(t)
	ctx := t.Context()
	service := templates.New(NewTemplateRepository(db))
	draft := configuration.TemplateDraft{ID: " branch / # "}
	created := issued.Add(15 * time.Minute)
	errs := make(chan error, 2)
	for range 2 {
		go func() { errs <- writer.Create(ctx, digest, fixedWebTime(created), draft) }()
	}
	one, two := <-errs, <-errs
	if !(one == nil && errors.Is(two, templates.ErrConflict) || two == nil && errors.Is(one, templates.ErrConflict)) {
		t.Fatalf("concurrent insert results: %v, %v", one, two)
	}
	saved, err := service.Get(ctx, draft.ID)
	if err != nil || saved.ID != draft.ID || saved.Version != 1 || len(saved.Sections) != 0 {
		t.Fatalf("saved empty draft: %#v %v", saved, err)
	}
	edited := configuration.TemplateDraft{ID: draft.ID, Sections: []configuration.TemplateSection{{Name: "outbounds"}}}
	if _, err := service.Replace(ctx, edited, 1); err != nil {
		t.Fatal(err)
	}
	if err := writer.Create(ctx, digest, fixedWebTime(issued.Add(35*time.Minute)), draft); !errors.Is(err, templates.ErrConflict) {
		t.Fatalf("duplicate before renewed expiry: %v", err)
	}
	saved, err = service.Get(ctx, draft.ID)
	if err != nil || saved.Version != 2 || len(saved.Sections) != 1 || saved.Sections[0].Name != "outbounds" {
		t.Fatalf("duplicate changed C02/C03 snapshot: %#v %v", saved, err)
	}
	draft.ID = "after-expiry"
	if err := writer.Create(ctx, digest, fixedWebTime(created.Add(webadmin.IdleLifetime)), draft); !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("duplicate unexpectedly renewed activity: %v", err)
	}
	if _, err := service.Get(ctx, draft.ID); !errors.Is(err, templates.ErrNotFound) {
		t.Fatalf("created with expired session: %v", err)
	}
}

func TestWebTemplateCreateChecksSessionAfterAcquiringWriter(t *testing.T) {
	for _, mode := range []string{"revoked", "idle", "absolute"} {
		t.Run(mode, func(t *testing.T) {
			db, writer, digest, issued := fixtureWebTemplateWriter(t)
			ctx := t.Context()
			now := issued.Add(time.Minute)
			expired := issued.Add(webadmin.IdleLifetime)
			if mode == "absolute" {
				sessions := NewWebAdmin(db)
				for d := 29 * time.Minute; d < webadmin.AbsoluteLifetime; d += 29 * time.Minute {
					if err := sessions.Session(ctx, digest, fixedWebTime(issued.Add(d)), webadmin.SessionTouch); err != nil {
						t.Fatal(err)
					}
					now = issued.Add(d)
				}
				expired = issued.Add(webadmin.AbsoluteLifetime)
			}
			blocker, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			if mode == "revoked" {
				if _, err := blocker.ExecContext(ctx, "DELETE FROM web_sessions WHERE digest = ?", digest[:]); err != nil {
					t.Fatal(err)
				}
			}
			var useLateClock atomic.Bool
			clockCalled := make(chan struct{}, 1)
			outcome := make(chan error, 1)
			draft := configuration.TemplateDraft{ID: "queued-example"}
			go func() {
				outcome <- writer.Create(ctx, digest, func() time.Time {
					clockCalled <- struct{}{}
					if mode != "revoked" && useLateClock.Load() {
						return expired
					}
					return now
				}, draft)
			}()
			select {
			case <-clockCalled:
				t.Fatal("session clock sampled before SQLite writer available")
			case <-time.After(30 * time.Millisecond):
			}
			useLateClock.Store(true)
			if err := blocker.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := <-outcome; !errors.Is(err, webadmin.ErrSignedOut) {
				t.Fatalf("queued create: %v", err)
			}
			if _, err := NewTemplateRepository(db).Find(ctx, draft.ID); !errors.Is(err, templates.ErrNotFound) {
				t.Fatalf("created from old session: %v", err)
			}
		})
	}
}
