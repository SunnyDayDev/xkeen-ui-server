package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestTemplatesMigrationFromC01(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, createMigrationHistory)
	execSQL(t, db, "INSERT INTO schema_migrations VALUES (1)")
	execSQL(t, db, "CREATE TABLE c01_probe (value TEXT); INSERT INTO c01_probe VALUES ('demo.example')")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := readText(t, db, "SELECT count(*) FROM schema_migrations"); got != fmt.Sprint(len(migrations)) {
		t.Errorf("history = %q", got)
	}
	if got := readText(t, db, "SELECT count(*) FROM schema_migrations WHERE version=2"); got != "1" {
		t.Errorf("migration v2 count = %q", got)
	}
	if got := readText(t, db, "SELECT value FROM c01_probe"); got != "demo.example" {
		t.Errorf("prior data = %q", got)
	}
	if _, err := templates.New(NewTemplateRepository(db)).Create(t.Context(), configuration.TemplateDraft{ID: "migrated"}); err != nil {
		t.Fatal(err)
	}
}

func TestTemplatesConcurrentCreateAndReopen(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "drafts.sqlite")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	type outcome struct {
		name string
		err  error
	}
	start := make(chan struct{})
	done := make(chan outcome, 2)
	for i, repo := range []TemplateRepository{NewTemplateRepository(db), NewTemplateRepository(second)} {
		name := []string{"first", "second"}[i]
		go func() {
			<-start
			_, err := templates.New(repo).Create(ctx, configuration.TemplateDraft{
				ID: "same", Sections: []configuration.TemplateSection{{Name: name}},
			})
			done <- outcome{name, err}
		}()
	}
	close(start)
	var saved string
	var conflicts int
	for range 2 {
		res := <-done
		switch {
		case res.err == nil:
			saved = res.name
		case errors.Is(res.err, templates.ErrConflict):
			conflicts++
		default:
			t.Errorf("create %s: %v", res.name, res.err)
		}
	}
	if saved == "" || conflicts != 1 {
		t.Fatalf("saved = %q, conflicts = %d", saved, conflicts)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s := templates.New(NewTemplateRepository(reopened))
	got, err := s.Get(ctx, "same")
	if err != nil || got.Version != 1 || len(got.Sections) != 1 || got.Sections[0].Name != saved {
		t.Fatalf("got %#v, %v; want winning draft %q", got, err, saved)
	}
	if _, err := reopened.Exec("UPDATE templates SET draft = ? WHERE id = ?", []byte(`{"private":"demo-private-value"}`), "same"); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, "same")
	if got != nil || !errors.Is(err, templates.ErrCorrupt) || strings.Contains(err.Error(), "demo-private-value") {
		t.Errorf("corrupt draft: %#v, %v", got, err)
	}
	for _, id := range []string{"Same", "same\x00", " same "} {
		if _, err := s.Create(ctx, configuration.TemplateDraft{ID: id}); err != nil {
			t.Errorf("distinct exact ID %q: %v", id, err)
		}
	}
}

func TestTemplatesPreserveEditingState(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "editing.sqlite")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := templates.New(NewTemplateRepository(db))
	content := " \n{ \"z\":1e+03, \"a\":\"\\u0061\", \"n\":9007199254740993, \"port\":\"${port}\", \"_xkeenUiId\":\"authored\" }\r\n"
	draft := configuration.TemplateDraft{ID: "editing", Sections: []configuration.TemplateSection{
		{Name: "stats"},
		{Name: "vendor/custom", Elements: []configuration.TemplateElement{
			{Element: configuration.Element{ID: "550e8400-e29b-41d4-a716-446655440000", Content: "null"}},
		}},
		{Name: "rules", Elements: []configuration.TemplateElement{
			{Disabled: true, Element: configuration.Element{
				ID: "550e8400-e29b-41d4-a716-446655440002", Content: content,
				Variables: []configuration.VariableDefinition{
					{Name: "missing", Type: "string"},
					{Name: "number", Type: "number", Default: json.RawMessage("9007199254740993")},
					{Name: "nullable", Type: "object", Default: json.RawMessage("null")},
				},
			}},
			{Element: configuration.Element{ID: "550e8400-e29b-41d4-a716-446655440001", Content: `"${partial} suffix"`}},
		}},
	}}
	if _, err := s.Create(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s = templates.New(NewTemplateRepository(reopened))
	got, err := s.Get(ctx, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	draft.Sections[0].Elements = []configuration.TemplateElement{}
	draft.Sections[1].Elements[0].Element.Variables = []configuration.VariableDefinition{}
	draft.Sections[2].Elements[1].Element.Variables = []configuration.VariableDefinition{}
	draft.Sections[2].Elements[0].Element.Variables = append(draft.Sections[2].Elements[0].Element.Variables, configuration.VariableDefinition{
		Name: "port", Type: "string", Default: json.RawMessage(`""`),
	})
	if got.Version != 1 || !reflect.DeepEqual(got.TemplateDraft, draft) {
		t.Fatalf("draft changed: %#v", got)
	}
	got.Sections[2].Elements[0].Element.Variables[1].Default[0] = '0'
	got.Sections[0].Name = "edited"
	again, err := s.Get(ctx, draft.ID)
	if err != nil || again.Version != 1 || !reflect.DeepEqual(again.TemplateDraft, draft) {
		t.Fatalf("mutated result changed re-read: %#v, %v", again, err)
	}

	// Simulate an existing record created before declarations were discovered.
	captured := []byte(`[{"name":"log","elements":[{"disabled":false,"record":{"id":"550e8400-e29b-41d4-a716-446655440004","content":"\"${unrecorded}\"","variables":[]}}]}]`)
	if _, err := reopened.Exec("INSERT INTO templates (id, draft) VALUES (?, ?)", "captured", captured); err != nil {
		t.Fatal(err)
	}
	preserved, err := s.Get(ctx, "captured")
	if err != nil || len(preserved.Sections[0].Elements[0].Element.Variables) != 0 {
		t.Errorf("read rediscovered variables: %#v, %v", preserved, err)
	}
}

func TestTemplatesRejectCorruptEnvelope(t *testing.T) {
	for _, raw := range []string{
		`[{"name":"log","NAME":"rules","elements":[]}]`,
		`[{"name":"log","elements":[],"elements":[]}]`,
		`[{"name":"log","elements":[]}] []`,
		`[{"name":"log","elements":null}]`,
		`[{"name":"log","elements":[{"record":{"id":"550e8400-e29b-41d4-a716-446655440004","content":"null","variables":[]}}]}]`,
	} {
		got, err := decodeTemplate("demo", []byte(raw))
		if got != nil || !errors.Is(err, templates.ErrCorrupt) {
			t.Errorf("decode %s: %#v, %v", raw, got, err)
		}
	}
}

func TestVerbatimDefaultMigration(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "defaults.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	prior := migrations[:14]
	if err := migrate(ctx, db, prior); err != nil {
		t.Fatal(err)
	}
	const uuid = "550e8400-e29b-41d4-a716-446655440000"
	const legacy = `[{"name":"rules","elements":[{"disabled":false,"record":{"id":"` + uuid + `","content":"{}","variables":[{"name":"x","type":"number","default":1}]}}]}]`
	if _, err = db.Exec("INSERT INTO templates(id,draft) VALUES (?,?)", "test.example", []byte(legacy)); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenExisting(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := readText(t, db, "SELECT CAST(draft AS TEXT) FROM templates"); got != legacy {
		t.Fatal("migration rewrote legacy source")
	}
	store := templates.New(NewTemplateRepository(db))
	got, err := store.Get(ctx, "test.example")
	if err != nil || string(got.Sections[0].Elements[0].Element.Variables[0].Default) != "1" {
		t.Fatal(got, err)
	}
	if err = migrate(ctx, db, prior); err == nil || !strings.Contains(err.Error(), "exceeds supported version 14") {
		t.Fatal("previous binary accepted current schema", err)
	}
}
