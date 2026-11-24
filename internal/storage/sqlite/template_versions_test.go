package sqlite

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestTemplateVersionsMigrateExistingDrafts(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "c02.sqlite")
	prior, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer prior.Close()
	if err := migrate(ctx, prior, migrations[:2]); err != nil {
		t.Fatal(err)
	}
	authored := []byte(`[{"name":"log","elements":[{"disabled":false,"record":{"id":"550e8400-e29b-41d4-a716-446655440000","content":" \n{\"z\":1e+03,\"n\":9007199254740993}\r\n","variables":[]}}]}]`)
	for id, blob := range map[string][]byte{"first": authored, "second": []byte("[]")} {
		if _, err := prior.ExecContext(ctx, "INSERT INTO templates (id, draft) VALUES (?, ?)", id, blob); err != nil {
			t.Fatal(err)
		}
	}
	if err := prior.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := templates.New(NewTemplateRepository(db))
	first, err := service.Get(ctx, "first")
	if err != nil || first == nil || first.Version != 1 || first.ID != "first" ||
		len(first.Sections) != 1 || len(first.Sections[0].Elements) != 1 ||
		first.Sections[0].Elements[0].Element.Content != " \n{\"z\":1e+03,\"n\":9007199254740993}\r\n" {
		t.Fatalf("migrated first draft = %#v, %v", first, err)
	}
	second, err := service.Get(ctx, "second")
	if err != nil || second == nil || second.Version != 1 || second.ID != "second" || len(second.Sections) != 0 {
		t.Fatalf("migrated second draft = %#v, %v", second, err)
	}
	var got []byte
	if err := db.QueryRowContext(ctx, "SELECT draft FROM templates WHERE id = 'first'").Scan(&got); err != nil || !bytes.Equal(got, authored) {
		t.Fatalf("migration changed existing draft bytes: %q, %v", got, err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE templates SET version = 1.5 WHERE id = 'first'"); err == nil {
		t.Error("version column accepted a non-integer")
	}
}

func TestTemplateVersionsReplaceAndReopen(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "templates.sqlite")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := templates.New(NewTemplateRepository(db))
	initial, err := service.Create(ctx, configuration.TemplateDraft{ID: "demo", Sections: []configuration.TemplateSection{
		{Name: "removed"},
		{Name: "rules", Elements: []configuration.TemplateElement{
			{Element: configuration.Element{ID: "550e8400-e29b-41d4-a716-446655440000", Content: "{}"}},
		}},
	}})
	if err != nil || initial == nil || initial.Version != 1 {
		t.Fatalf("initial version = %#v, %v", initial, err)
	}
	discarded := initial.Sections[1].Elements[0].Element.ID
	candidate := configuration.TemplateDraft{ID: initial.ID, Sections: []configuration.TemplateSection{
		{Name: "rules", Elements: []configuration.TemplateElement{
			{Disabled: true, Element: configuration.Element{
				ID: "550e8400-e29b-41d4-a716-446655440001", Content: " {\"value\":\"${demo}\"}\n",
			}},
		}},
		{Name: "stats"},
	}}
	saved, err := service.Replace(ctx, candidate, initial.Version)
	if err != nil || saved == nil || saved.ID != initial.ID || saved.Version != 2 || len(saved.Sections) != 2 || saved.Sections[1].Name != "stats" {
		t.Fatalf("replace = %#v, %v", saved, err)
	}
	if next := saved.Sections[0].Elements[0]; next.Element.ID == discarded || !next.Disabled || len(next.Element.Variables) != 1 {
		t.Fatalf("replacement element = %#v", next)
	}
	saved, err = service.Replace(ctx, saved.TemplateDraft, saved.Version)
	if err != nil || saved == nil || saved.Version != 3 {
		t.Fatalf("same content replacement = %#v, %v", saved, err)
	}
	for _, test := range []struct {
		candidate configuration.TemplateDraft
		expected  int64
		want      error
	}{
		{initial.TemplateDraft, initial.Version, templates.ErrVersionConflict},
		{initial.TemplateDraft, saved.Version + 1, templates.ErrVersionConflict},
		{configuration.TemplateDraft{ID: "unknown"}, saved.Version, templates.ErrNotFound},
	} {
		snapshot, err := service.Replace(ctx, test.candidate, test.expected)
		if snapshot != nil || !errors.Is(err, test.want) {
			t.Fatalf("replace rejected = %#v, %v, want %v", snapshot, err, test.want)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := templates.New(NewTemplateRepository(reopened)).Get(ctx, candidate.ID)
	if err != nil || got == nil || got.Version != 3 || len(got.Sections) != 2 || got.Sections[1].Name != "stats" ||
		len(got.Sections[0].Elements) != 1 ||
		got.Sections[0].Elements[0].Element.Content != candidate.Sections[0].Elements[0].Element.Content {
		t.Fatalf("reopened draft = %#v, %v", got, err)
	}
}

func TestTemplateVersionsConcurrentReplace(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "same.sqlite")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	id := "same"
	initial, err := templates.New(NewTemplateRepository(first)).Create(ctx, configuration.TemplateDraft{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		name  string
		saved *templates.Snapshot
		err   error
	}
	start := make(chan struct{})
	done := make(chan outcome, 2)
	for i, db := range []*sql.DB{first, second} {
		name := []string{"first", "second"}[i]
		go func() {
			<-start
			service := templates.New(NewTemplateRepository(db))
			saved, err := service.Replace(ctx, configuration.TemplateDraft{
				ID: id, Sections: []configuration.TemplateSection{{Name: name}},
			}, initial.Version)
			done <- outcome{name, saved, err}
		}()
	}
	close(start)
	var winner string
	var conflicts int
	for range 2 {
		res := <-done
		switch {
		case res.err == nil && res.saved != nil && res.saved.Version == 2:
			winner = res.name
		case res.saved == nil && errors.Is(res.err, templates.ErrVersionConflict):
			conflicts++
		default:
			t.Errorf("replace %s = %#v, %v", res.name, res.saved, res.err)
		}
	}
	if winner == "" || conflicts != 1 {
		t.Fatalf("winner = %q, conflicts = %d", winner, conflicts)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := templates.New(NewTemplateRepository(reopened)).Get(ctx, id)
	if err != nil || got == nil || got.Version != 2 || len(got.Sections) != 1 || got.Sections[0].Name != winner {
		t.Fatalf("reopened winning draft = %#v, %v; want %q at 2", got, err, winner)
	}
}

func TestTemplateVersionsExhaustionAndWriteFailure(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "failures.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := templates.New(NewTemplateRepository(db))
	initial, err := service.Create(ctx, configuration.TemplateDraft{
		ID: "demo", Sections: []configuration.TemplateSection{{Name: "before"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	next := configuration.TemplateDraft{ID: initial.ID, Sections: []configuration.TemplateSection{{Name: "demo-private-value"}}}
	execSQL(t, db, "PRAGMA query_only=ON")
	if got, err := service.Replace(ctx, next, initial.Version); got != nil || err == nil ||
		errors.Is(err, templates.ErrVersionConflict) || strings.Contains(err.Error(), "demo-private-value") {
		t.Fatalf("write failure = %#v, %v", got, err)
	}
	got, err := service.Get(ctx, initial.ID)
	if err != nil || got == nil || got.Version != initial.Version || got.Sections[0].Name != "before" {
		t.Fatalf("write failure changed draft: %#v, %v", got, err)
	}
	execSQL(t, db, "PRAGMA query_only=OFF")
	if _, err := db.ExecContext(ctx, "UPDATE templates SET version = ? WHERE id = ?", int64(math.MaxInt64), initial.ID); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := service.Replace(ctx, next, math.MaxInt64-1); snapshot != nil || !errors.Is(err, templates.ErrVersionConflict) {
		t.Fatalf("stale max version = %#v, %v", snapshot, err)
	}
	if snapshot, err := service.Replace(ctx, next, math.MaxInt64); snapshot != nil || !errors.Is(err, templates.ErrVersionExhausted) {
		t.Fatalf("exhausted version = %#v, %v", snapshot, err)
	}
	got, err = service.Get(ctx, initial.ID)
	if err != nil || got == nil || got.Version != math.MaxInt64 || got.Sections[0].Name != "before" {
		t.Fatalf("exhausted update changed draft: %#v, %v", got, err)
	}
}

func TestTemplateVersionsPreserveRouterDrafts(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "c04.sqlite")
	prior, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer prior.Close()
	if err := migrate(ctx, prior, migrations[:3]); err != nil {
		t.Fatal(err)
	}
	oldID := "550e8400-e29b-41d4-a716-446655440000"
	oldDraft := []byte(`[{"name":"rules","elements":[{"disabled":false,"record":{"id":"550e8400-e29b-41d4-a716-446655440000","content":"{}","variables":[]}}]}]`)
	if _, err := prior.ExecContext(ctx, "INSERT INTO templates (id,draft) VALUES (?,?)", "shared", oldDraft); err != nil {
		t.Fatal(err)
	}
	router := routersettings.Draft{
		ID: "router.example", Name: "Demo", TemplateID: "shared", PersonalOrder: []string{oldID},
		Groups: []routersettings.Group{{
			Name: "rules", Exclusions: []configuration.ElementSource{{TemplateID: "shared", ElementID: oldID}},
		}},
	}
	rawRouter, err := json.Marshal(encodeRouterDraft(router))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prior.Exec("INSERT INTO router_settings(id,snapshot) VALUES (?,?)", router.ID, rawRouter); err != nil {
		t.Fatal(err)
	}
	if err := prior.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if count := readText(t, db, "SELECT count(*) FROM schema_migrations WHERE version = 4"); count != "1" {
		t.Fatalf("v4 is missing from migration history")
	}
	service := templates.New(NewTemplateRepository(db))
	initial, err := service.Get(ctx, "shared")
	if err != nil || initial == nil || initial.Version != 1 {
		t.Fatalf("migrated template = %#v, %v", initial, err)
	}
	replacement := initial.TemplateDraft
	replacement.Sections[0].Elements[0].Element.ID = "550e8400-e29b-41d4-a716-446655440001"
	if _, err := service.Replace(ctx, replacement, initial.Version); err != nil {
		t.Fatal(err)
	}
	got, found, err := routersettings.NewService(NewRouterSettings(db)).Get(ctx, router.ID)
	if err != nil || !found || !reflect.DeepEqual(got, router) {
		t.Fatalf("migration or replacement changed saved router: %#v, found %t, %v", got, found, err)
	}
}
