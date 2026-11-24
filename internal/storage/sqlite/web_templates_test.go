package sqlite

import (
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
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
