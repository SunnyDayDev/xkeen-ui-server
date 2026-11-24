package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/validation"
)

func TestSharedTemplatePublicationWithSQLite(t *testing.T) {
	checkSharedTemplatePublication(t, testPublicationValidator(func(_ context.Context, profile string, files []configuration.CatalogFile) error {
		if profile != configuration.XrayIdentityVersion || len(files) != 5 {
			t.Fatal("wrong catalog for validation")
		}
		for _, file := range files {
			if bytes.Contains(file.Content, []byte("invalid-demo-protocol")) {
				return publication.ErrCatalogRejected
			}
		}
		return nil
	}))
}

func TestSharedTemplatePublicationWithIsolatedXray(t *testing.T) {
	socket := os.Getenv("XKEEN_TEST_VALIDATOR_SOCKET")
	if socket == "" {
		t.Skip("set XKEEN_TEST_VALIDATOR_SOCKET to a private socket for the isolated Xray worker")
	}
	validator, err := validation.Local(socket)
	if err != nil {
		t.Fatal(err)
	}
	checkSharedTemplatePublication(t, validator)
}

func checkSharedTemplatePublication(t *testing.T, validator publication.CatalogValidator) {
	t.Helper()
	ctx := t.Context()
	db, path := openPublicationDB(t)
	other, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	publications := NewPublications(db)
	command := publication.NewTemplateGroupService(publications, validator)
	single := publication.NewService(NewPublications(other), validator)
	templateDrafts := templates.New(NewTemplateRepository(other))
	routerDrafts := routersettings.NewService(NewRouterSettings(other))
	const (
		profileID   = "shared-template"
		outboundID  = "550e8400-e29b-41d4-a716-446655440030"
		ruleID      = "550e8400-e29b-41d4-a716-446655440031"
		nextRuleID  = "550e8400-e29b-41d4-a716-446655440032"
		replacerID  = "a-replacer.example"
		inheritorID = "b-inheritor.example"
	)
	newTemplate := func(protocol string) configuration.TemplateDraft {
		return configuration.TemplateDraft{ID: profileID, Sections: []configuration.TemplateSection{
			{Name: "outbounds", Elements: []configuration.TemplateElement{{Element: configuration.Element{
				ID: outboundID, Content: `{"protocol":"` + protocol + `","tag":"direct"}`,
			}}}},
			{Name: "rules", Elements: []configuration.TemplateElement{{Element: configuration.Element{
				ID: ruleID, Content: `{"type":"field","network":"tcp","outboundTag":"direct"}`,
			}}}},
		}}
	}
	initial, err := templateDrafts.Create(ctx, newTemplate("freedom"))
	if err != nil {
		t.Fatal(err)
	}
	// The first router keeps its own outbound and excludes a rule from its catalog.
	replacer := routersettings.Draft{ID: replacerID, TemplateID: profileID, PersonalOrder: []string{ruleID},
		Groups: []routersettings.Group{{Name: "rules", Exclusions: []configuration.ElementSource{{TemplateID: profileID, ElementID: ruleID}}}, {Name: "outbounds", Overrides: []configuration.ContentOverride{{
			Source: configuration.ElementSource{TemplateID: profileID, ElementID: outboundID}, Mode: configuration.ContentModeReplacement,
			Content: json.RawMessage(`{"protocol":"freedom","tag":"direct"}`),
		}}}},
	}
	basis := &publication.OrderBasis{TemplateID: profileID, TemplateVersion: 1, SharedRuleIDs: []string{ruleID}}
	outcome, err := single.Publish(ctx, publication.PublishRequest{
		Mode: publication.ConfirmInitial, Source: publication.SourceCandidate, Candidate: &replacer, OrderBasis: basis,
		Expected: publication.Expectations{RouterID: replacer.ID, TemplateID: profileID, TemplateDraftVersion: &initial.Version},
	})
	requirePublicationOutcome(t, outcome, err, 1, true)
	inheritor := routersettings.Draft{ID: inheritorID, TemplateID: profileID}
	outcome, err = single.Publish(ctx, publication.PublishRequest{
		Mode: publication.ConfirmAccepted, Source: publication.SourceCandidate, Candidate: &inheritor,
		Expected: publication.Expectations{RouterID: inheritor.ID, TemplateID: profileID, AcceptedVersion: 1},
	})
	requirePublicationOutcome(t, outcome, err, 1, true)

	pending := routersettings.Draft{ID: replacer.ID, TemplateID: "different"}
	if err := routerDrafts.Save(ctx, pending); err != nil {
		t.Fatal(err)
	}
	edited := newTemplate("blackhole")
	editRequest := publication.SharedPublishRequest{
		TemplateID: profileID, Source: publication.SourceSharedCandidate, Candidate: &edited,
		TemplateDraftVersion: 1, AcceptedVersion: 1,
	}
	pendingWhileChecking := testPublicationValidator(func(ctx context.Context, profile string, files []configuration.CatalogFile) error {
		pending.Name = "changed separately"
		if err := routerDrafts.Save(ctx, pending); err != nil {
			t.Fatal(err)
		}
		return validator.Validate(ctx, profile, files)
	})
	result, err := publication.NewTemplateGroupService(publications, pendingWhileChecking).Publish(ctx, editRequest)
	want := &publication.TemplateGroupResult{TemplateID: profileID, Version: 2, Routers: []publication.GroupRouterResult{
		{RouterID: replacerID, Revision: 1}, {RouterID: inheritorID, Revision: 2, Changed: true},
	}}
	if err != nil || !reflect.DeepEqual(result, want) {
		t.Fatalf("shared publish = %#v %v, want %#v", result, err, want)
	}

	snapshot := func() *publication.TemplateGroupInputs {
		t.Helper()
		value, err := publications.ReadTemplateGroup(ctx, profileID)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	beforeInvalid := snapshot()
	for _, member := range beforeInvalid.Members {
		var routing []byte
		for _, file := range member.Desired.Catalog.Files {
			if file.Name == "05_routing.json" {
				routing = file.Content
				break
			}
		}
		inheritsRule := member.Active.Draft.ID == inheritorID
		if routing == nil || bytes.Contains(routing, []byte(ruleID)) != inheritsRule {
			t.Fatalf("unexpected routing catalog for %s", member.Active.Draft.ID)
		}
	}
	if beforeInvalid.Members[0].Active.OrderBasis.TemplateVersion != 2 {
		t.Fatal("unchanged catalog did not advance active order basis")
	}
	if got, ok, err := routerDrafts.Get(ctx, pending.ID); err != nil || !ok || !reflect.DeepEqual(got, pending) {
		t.Fatalf("unrelated draft = %#v, %t, %v", got, ok, err)
	}

	rejected := newTemplate("invalid-demo-protocol")
	editRequest.Candidate, editRequest.AcceptedVersion, editRequest.TemplateDraftVersion = &rejected, 2, 2
	got, err := command.Publish(ctx, editRequest)
	var memberError *publication.GroupMemberError
	if got != nil || !errors.Is(err, publication.ErrCatalogRejected) || !errors.As(err, &memberError) || memberError.RouterID != inheritorID ||
		!reflect.DeepEqual(snapshot(), beforeInvalid) {
		t.Fatalf("late rejection = %#v, %v, member=%#v", got, err, memberError)
	}

	// Saving the common version alone does not publish it. A new assignment
	// uses the accepted snapshot while the group is validating the draft.
	newOrder := newTemplate("blackhole")
	newOrder.Sections[1].Elements = append([]configuration.TemplateElement{{Element: configuration.Element{
		ID: nextRuleID, Content: `{"type":"field","network":"udp","outboundTag":"direct"}`,
	}}}, newOrder.Sections[1].Elements...)
	stored, err := templateDrafts.Replace(ctx, newOrder, 2)
	if err != nil {
		t.Fatal(err)
	}
	savedRequest := publication.SharedPublishRequest{
		TemplateID: profileID, Source: publication.SourceSharedDraft, TemplateDraftVersion: stored.Version, AcceptedVersion: 2,
	}
	late := routersettings.Draft{ID: "c-late.example", TemplateID: profileID}
	checked := 0
	withPhantom := testPublicationValidator(func(ctx context.Context, profile string, files []configuration.CatalogFile) error {
		if checked == 0 {
			got, err := single.Publish(ctx, publication.PublishRequest{
				Mode: publication.ConfirmAccepted, Source: publication.SourceCandidate, Candidate: &late,
				Expected: publication.Expectations{RouterID: late.ID, TemplateID: profileID, AcceptedVersion: 2},
			})
			requirePublicationOutcome(t, got, err, 1, true)
		}
		checked++
		return validator.Validate(ctx, profile, files)
	})
	if got, err := publication.NewTemplateGroupService(publications, withPhantom).Publish(ctx, savedRequest); got != nil ||
		!errors.Is(err, publication.ErrConflict) || checked != 2 {
		t.Fatalf("phantom publish = %#v, %v; checked %d original members", got, err, checked)
	}
	afterConflict := snapshot()
	if afterConflict.Accepted.Version != 2 || afterConflict.TemplateDraft.Version != 3 || len(afterConflict.Members) != 3 ||
		!reflect.DeepEqual(afterConflict.Members[:2], beforeInvalid.Members) {
		t.Fatalf("phantom conflict changed accepted state: %#v", afterConflict)
	}
	final, err := command.Publish(ctx, savedRequest)
	if err != nil || final.Version != 3 || len(final.Routers) != 3 {
		t.Fatalf("publish saved = %#v, %v", final, err)
	}
	state := snapshot()
	if state.TemplateDraft.Version != 3 || state.Accepted.Version != 3 ||
		!reflect.DeepEqual(state.Members[0].Active.Draft.PersonalOrder, []string{nextRuleID, ruleID}) ||
		state.Members[0].Active.OrderBasis.TemplateVersion != 3 {
		t.Fatalf("stored saved version and complete personal order = %#v", state)
	}

	empty, err := templateDrafts.Create(ctx, configuration.TemplateDraft{ID: "no-assignments"})
	if err != nil {
		t.Fatal(err)
	}
	none, err := publication.NewTemplateGroupService(publications, nil).Publish(ctx, publication.SharedPublishRequest{
		TemplateID: empty.ID, Source: publication.SourceSharedDraft, TemplateDraftVersion: empty.Version,
	})
	if err != nil || none == nil || none.Version != 1 || none.Routers == nil || len(none.Routers) != 0 {
		t.Fatalf("empty publish = %#v %v", none, err)
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
	finalSnapshot, err := NewPublications(reopened).ReadTemplateGroup(ctx, profileID)
	if err != nil || !reflect.DeepEqual(finalSnapshot, state) {
		t.Fatalf("reopened group differs: %#v %v", finalSnapshot, err)
	}
}
