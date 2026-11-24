package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type testPublicationValidator func(context.Context, string, []configuration.CatalogFile) error

func (f testPublicationValidator) Validate(ctx context.Context, profile string, files []configuration.CatalogFile) error {
	return f(ctx, profile, files)
}

func TestSingleRouterPublicationWithSQLite(t *testing.T) {
	checkSingleRouterPublication(t, testPublicationValidator(func(_ context.Context, profile string, files []configuration.CatalogFile) error {
		if profile != configuration.XrayIdentityVersion || len(files) != 5 {
			t.Fatalf("checked profile=%q files=%#v", profile, files)
		}
		for _, file := range files {
			if bytes.Contains(file.Content, []byte("invalid-demo-protocol")) {
				return publication.ErrCatalogRejected
			}
		}
		return nil
	}))
}

func checkSingleRouterPublication(t *testing.T, validator publication.CatalogValidator) {
	t.Helper()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "publication.sqlite")
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
	store := NewPublications(db)
	routerDrafts := routersettings.NewService(NewRouterSettings(other))
	templateDrafts := templates.New(NewTemplateRepository(other))
	id, templateID := "router.example", "template-demo"
	template, err := templateDrafts.Create(ctx, configuration.TemplateDraft{ID: templateID, Sections: []configuration.TemplateSection{
		{Name: "outbounds", Elements: []configuration.TemplateElement{
			{Element: configuration.Element{
				ID: "550e8400-e29b-41d4-a716-446655440000", Content: `{"protocol":"freedom","tag":"direct"}`,
			}},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	first := routersettings.Draft{ID: id, Name: "first", TemplateID: templateID}
	service := publication.NewService(store, validator)
	request := publication.PublishRequest{
		Mode: publication.ConfirmInitial, Source: publication.SourceCandidate, Candidate: &first,
		Expected: publication.Expectations{RouterID: id, TemplateID: templateID, TemplateDraftVersion: &template.Version},
	}
	result, err := service.Publish(ctx, request)
	requirePublicationOutcome(t, result, err, 1, true)

	read := func(withDraft bool) *publication.Inputs {
		t.Helper()
		value, err := store.Read(ctx, publication.ReadRequest{RouterID: id, IncludeRouterDraft: withDraft})
		if err != nil || value == nil || value.Active == nil || value.Accepted == nil || value.Desired == nil {
			t.Fatalf("read publication = %#v, %v", value, err)
		}
		return value
	}
	initial := read(false)
	if initial.Accepted.Version != 1 || initial.Active.Draft.Name != "first" {
		t.Fatalf("initial acceptance = %#v", initial)
	}

	// A newer independently saved template must not enter this router's publication.
	edited := template.TemplateDraft
	edited.Sections[0].Elements[0].Element.Content = `{"protocol":"invalid-demo-protocol","tag":"editable-only"}`
	if _, err := templateDrafts.Replace(ctx, edited, template.Version); err != nil {
		t.Fatal(err)
	}
	pending := routersettings.Draft{ID: id, Name: "pending", TemplateID: "different"}
	if err := routerDrafts.Save(ctx, pending); err != nil {
		t.Fatal(err)
	}

	ordinary := func(in *publication.Inputs, source publication.PersonalSource) publication.PublishRequest {
		return publication.PublishRequest{Mode: publication.PublishOrdinary, Source: source, Expected: publication.Expectations{
			RouterID: id, TemplateID: templateID, AcceptedVersion: in.Accepted.Version,
			ActiveGeneration: in.Active.Generation, DesiredNumber: in.Desired.Number,
		}}
	}
	// A new active label produces the same catalog; an unrelated edit is free to commit meanwhile.
	equal := first
	equal.Name = "active renamed"
	request = ordinary(initial, publication.SourceCandidate)
	request.Candidate = &equal
	withPendingEdit := testPublicationValidator(func(ctx context.Context, profile string, files []configuration.CatalogFile) error {
		pending.Name = "edited while checking"
		if err := routerDrafts.Save(ctx, pending); err != nil {
			t.Fatal(err)
		}
		return validator.Validate(ctx, profile, files)
	})
	result, err = publication.NewService(store, withPendingEdit).Publish(ctx, request)
	requirePublicationOutcome(t, result, err, 1, false)
	afterEqual := read(true)
	if afterEqual.Active.Draft.Name != equal.Name || afterEqual.RouterDraft == nil || !reflect.DeepEqual(afterEqual.RouterDraft.Draft, pending) ||
		afterEqual.Accepted.Version != 1 || !reflect.DeepEqual(afterEqual.Desired, initial.Desired) {
		t.Fatalf("equal publication changed pending or desired state: %#v", afterEqual)
	}

	changed := equal
	changed.Groups = []routersettings.Group{{Name: "outbounds", Local: []routersettings.LocalElement{
		{ID: "550e8400-e29b-41d4-a716-446655440001", Content: json.RawMessage(`{"protocol":"freedom","tag":"local"}`)},
	}}}
	request = ordinary(afterEqual, publication.SourceCandidate)
	request.Candidate = &changed
	result, err = service.Publish(ctx, request)
	requirePublicationOutcome(t, result, err, 2, true)
	afterChange := read(true)

	invalid := changed
	invalid.Groups = []routersettings.Group{{Name: "outbounds", Local: []routersettings.LocalElement{
		{ID: "550e8400-e29b-41d4-a716-446655440001", Content: json.RawMessage(`{"protocol":"invalid-demo-protocol","tag":"declined"}`)},
	}}}
	request = ordinary(afterChange, publication.SourceCandidate)
	request.Candidate = &invalid
	if got, err := service.Publish(ctx, request); got != nil || !errors.Is(err, publication.ErrCatalogRejected) {
		t.Fatalf("invalid publication = %#v, %v", got, err)
	}
	if got := read(true); !reflect.DeepEqual(got, afterChange) {
		t.Fatalf("rejected publication modified stored state: %#v", got)
	}

	// A used draft is subject to publication CAS even when the validator succeeds.
	if err := routerDrafts.Save(ctx, changed); err != nil {
		t.Fatal(err)
	}
	beforeConflict := read(true)
	request = ordinary(beforeConflict, publication.SourceSavedDraft)
	request.Expected.RouterDraftGeneration = &beforeConflict.RouterDraft.Generation
	editUsedDraft := testPublicationValidator(func(ctx context.Context, profile string, files []configuration.CatalogFile) error {
		newDraft := changed
		newDraft.Name = "newer saved draft"
		if err := routerDrafts.Save(ctx, newDraft); err != nil {
			t.Fatal(err)
		}
		return validator.Validate(ctx, profile, files)
	})
	if got, err := publication.NewService(store, editUsedDraft).Publish(ctx, request); got != nil || !errors.Is(err, publication.ErrConflict) {
		t.Fatalf("stale publication = %#v, %v", got, err)
	}
	final := read(true)
	if final.RouterDraft.Draft.Name != "newer saved draft" || !reflect.DeepEqual(final.Active, afterChange.Active) ||
		!reflect.DeepEqual(final.Desired, afterChange.Desired) {
		t.Fatalf("conflict changed accepted state: %#v", final)
	}
	if err := other.Close(); err != nil {
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
	got, err := NewPublications(reopened).Read(ctx, publication.ReadRequest{RouterID: id, IncludeRouterDraft: true})
	if err != nil || !reflect.DeepEqual(got, final) {
		t.Fatalf("reopened publication = %#v, %v", got, err)
	}
}

func requirePublicationOutcome(t *testing.T, got *publication.PublishResult, err error, revision int64, changed bool) {
	t.Helper()
	if err != nil || got == nil || got.Revision != revision || got.Changed != changed {
		t.Fatalf("publish = %#v, %v; want revision %d with changed=%t", got, err, revision, changed)
	}
}
