package sqlite

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

func TestWebAdminResetAndSessionCAS(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "web.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	store := NewWebAdmin(db)
	a := webadmin.Verifier{Version: 1, Salt: [16]byte{1}, Digest: [32]byte{1}}
	b := webadmin.Verifier{Version: 1, Salt: [16]byte{2}, Digest: [32]byte{2}}
	if err := store.SaveVerifier(ctx, a, true); !errors.Is(err, webadmin.ErrSetupRequired) {
		t.Fatalf("uninitialized reset: %v", err)
	}
	if err := store.SaveVerifier(ctx, a, false); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveVerifier(ctx, b, false); !errors.Is(err, webadmin.ErrAlreadySetup) {
		t.Fatalf("repeated setup: %v", err)
	}
	verifier, generation, err := store.Verifier(ctx)
	if err != nil || verifier != a || generation != 1 {
		t.Fatalf("verifier generation %d: %v", generation, err)
	}

	now := time.Unix(1000000000, 0)
	one, two, three := [32]byte{1}, [32]byte{2}, [32]byte{3}
	if err := store.Issue(ctx, generation, one, [32]byte{}, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Issue(ctx, generation, two, one, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Session(ctx, one, fixedWebTime(now), webadmin.SessionRead); !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("rotated cookie: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store = NewWebAdmin(db)
	if err := store.Session(ctx, two, fixedWebTime(now), webadmin.SessionRead); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveVerifier(ctx, b, true); err != nil {
		t.Fatal(err)
	}
	if err := store.Issue(ctx, generation, three, [32]byte{}, now); !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("old verifier issued session: %v", err)
	}
	if err := store.Session(ctx, two, fixedWebTime(now), webadmin.SessionTouch); !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("reset session: %v", err)
	}
}

func TestWebAdminExactSessionDeadlines(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewWebAdmin(db)
	if err := store.SaveVerifier(ctx, webadmin.Verifier{Version: 1}, false); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000000000, 0)
	token := [32]byte{1}
	if err := store.Issue(ctx, 1, token, [32]byte{}, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Session(ctx, token, fixedWebTime(now.Add(29*time.Minute)), webadmin.SessionRead); err != nil {
		t.Fatal(err)
	}
	if err := store.Session(ctx, token, fixedWebTime(now.Add(30*time.Minute)), webadmin.SessionTouch); !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("deadline activity: %v", err)
	}
	token[0]++
	if err := store.Issue(ctx, 1, token, [32]byte{}, now); err != nil {
		t.Fatal(err)
	}
	for d := 29 * time.Minute; d < 12*time.Hour; d += 29 * time.Minute {
		if err := store.Session(ctx, token, fixedWebTime(now.Add(d)), webadmin.SessionTouch); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Session(ctx, token, fixedWebTime(now.Add(12*time.Hour)), webadmin.SessionTouch); !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("absolute deadline: %v", err)
	}
}

func TestWebAdminQueuedActivityUsesTimeInsideWriter(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewWebAdmin(db)
	if err := store.SaveVerifier(ctx, webadmin.Verifier{Version: 1}, false); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000000000, 0)
	token := [32]byte{1}
	if err := store.Issue(ctx, 1, token, [32]byte{}, now); err != nil {
		t.Fatal(err)
	}
	held, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	checked := make(chan struct{}, 1)
	activity := make(chan error, 1)
	var expired atomic.Bool
	go func() {
		activity <- store.Session(ctx, token, func() time.Time {
			checked <- struct{}{}
			if expired.Load() {
				return now.Add(webadmin.IdleLifetime)
			}
			return now.Add(time.Minute)
		}, webadmin.SessionTouch)
	}()
	select {
	case <-checked:
		t.Fatal("deadline read before authorization acquired")
	case <-time.After(30 * time.Millisecond):
	}
	expired.Store(true)
	if err := held.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-activity; !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("queued activity renewed expired session: %v", err)
	}
}

func fixedWebTime(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestWebAdminMigratesPreservingExistingApplication(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "earlier.db")
	prior, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer prior.Close()
	if err := migrate(ctx, prior, migrations[:7]); err != nil {
		t.Fatal(err)
	}
	execSQL(t, prior, "INSERT INTO agent_registrations VALUES ('router-example', 4, 1)")
	if err := prior.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := readText(t, db, "SELECT generation FROM agent_registrations WHERE router_id='router-example'"); got != "4" {
		t.Fatalf("migrated enrollment generation = %q", got)
	}
	if _, _, err := NewWebAdmin(db).Verifier(ctx); !errors.Is(err, webadmin.ErrSetupRequired) {
		t.Fatalf("first web state: %v", err)
	}
}

func TestWebAdminConcurrentSetupAndAtomicResetFailure(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "web.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	other, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	stores := []*WebAdmin{NewWebAdmin(db), NewWebAdmin(other)}
	start, results := make(chan struct{}), make(chan error, 2)
	for i, store := range stores {
		go func() {
			<-start
			results <- store.SaveVerifier(ctx, webadmin.Verifier{Version: 1, Salt: [16]byte{byte(i + 1)}}, false)
		}()
	}
	close(start)
	a, b := <-results, <-results
	if !(a == nil && errors.Is(b, webadmin.ErrAlreadySetup) || b == nil && errors.Is(a, webadmin.ErrAlreadySetup)) {
		t.Fatalf("setup returned %v and %v", a, b)
	}
	active, version, err := stores[0].Verifier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000000000, 0)
	token := [32]byte{1}
	if err := stores[0].Issue(ctx, version, token, [32]byte{}, now); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "CREATE TRIGGER reject_web_session_delete BEFORE DELETE ON web_sessions BEGIN SELECT RAISE(ABORT, 'synthetic write failure'); END")
	if err := stores[1].SaveVerifier(ctx, webadmin.Verifier{Version: 1, Salt: [16]byte{3}}, true); err == nil {
		t.Fatal("reset reported success after failure to revoke sessions")
	}
	retained, after, err := stores[0].Verifier(ctx)
	if err != nil || retained != active || after != version {
		t.Fatalf("failed reset modified verifier: %v", err)
	}
	if err := stores[0].Session(ctx, token, fixedWebTime(now), webadmin.SessionRead); err != nil {
		t.Fatalf("failed reset invalidated prior session: %v", err)
	}
}
