package sqlite

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

func TestWebTemplateEditorReadsAndWritesExactScopedSnapshot(t *testing.T) {
	db, store, digest, issued := fixtureWebTemplateWriter(t)
	ctx := t.Context()
	const id = "exact / # "
	const selected = "550e8400-e29b-41d4-a716-446655440000"
	const untouched = "550e8400-e29b-41d4-a716-446655440001"
	legacy := []byte(`[{"name":"outbounds","elements":[{"disabled":false,"record":{"id":"` + selected + `","content":"{}","variables":[]}},{"disabled":true,"record":{"id":"` + untouched + `","content":"\"$` + "{old}" + `\"","variables":[]}}]}]`)
	if _, err := db.ExecContext(ctx, "INSERT INTO templates(id,draft) VALUES(?,?)", id, legacy); err != nil {
		t.Fatal(err)
	}
	base, err := store.ReadTemplate(ctx, digest, fixedWebTime(issued), id)
	if err != nil {
		t.Fatal(err)
	}
	const source = "{\r\n \"n\":9007199254740993\n}\r\n"
	edit, err := templates.EditElementContent(base, selected, source)
	if err != nil {
		t.Fatal(err)
	}
	versions := make(chan int64, 2)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			v, err := store.ReplaceTemplate(ctx, digest, fixedWebTime(issued.Add(time.Minute)), edit, 1)
			versions <- v
			results <- err
		}()
	}
	a, b := <-results, <-results
	v1, v2 := <-versions, <-versions
	if v1+v2 != 2 || (a != nil || !errors.Is(b, templates.ErrVersionConflict)) && (b != nil || !errors.Is(a, templates.ErrVersionConflict)) {
		t.Fatalf("concurrent replace: %d / %d, %v / %v", v1, v2, a, b)
	}
	saved, err := store.ReadTemplate(ctx, digest, fixedWebTime(issued.Add(time.Minute)), id)
	if err != nil || saved.Version != 2 || saved.Sections[0].Elements[0].Element.Content != source ||
		!reflect.DeepEqual(saved.Sections[0].Elements[1], base.Sections[0].Elements[1]) {
		t.Fatalf("snapshot or legacy sibling not retained: %v", err)
	}
	if err := NewWebAdmin(db).SaveVerifier(ctx, webadmin.Verifier{Version: 1}, true); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ReadTemplate(ctx, digest, fixedWebTime(issued.Add(time.Minute)), id); got != nil || !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("private read after reset: %v", err)
	}
	if _, err := store.ReplaceTemplate(ctx, digest, fixedWebTime(issued.Add(time.Minute)), edit, 2); !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("stale authorization permitted effect: %v", err)
	}
}
