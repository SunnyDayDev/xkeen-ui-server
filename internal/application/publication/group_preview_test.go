package publication

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestSharedPreviewRevalidatesCompleteCandidateWithoutWriting(t *testing.T) {
	input, request := sharedFixture(t, false)
	update := changedSharedFixture(input)
	input.TemplateDraft = &templates.Snapshot{TemplateDraft: update, Version: 2}
	request.TemplateDraftVersion = 2
	store := &groupStoreStub{inputs: input}
	checks := 0
	validator := catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error { checks++; return nil })
	service := NewTemplateGroupService(store, validator)
	preview, err := service.Preview(t.Context(), request.TemplateID, 2)
	if err != nil || preview.AcceptedVersion != 1 || preview.Version != 2 || len(preview.Members) != 2 || len(store.commits) != 0 || checks != 2 {
		t.Fatalf("dry run: %+v %v, checks=%d", preview, err, checks)
	}
	if got := preview.Members[0]; got.Expected.RouterID != "inheriting.example" || got.Estimate != "restart_expected" ||
		!reflect.DeepEqual(got.Changes, []CatalogFileChange{{Name: "04_outbounds.json", Kind: "changed"}}) {
		t.Fatalf("inheritor: %+v", got)
	}
	if got := preview.Members[1]; got.Expected.RouterID != "replacing.example" || got.Estimate != "unchanged" || len(got.Changes) != 0 {
		t.Fatalf("replacement: %+v", got)
	}
	request.PreviewFingerprint = &preview.Fingerprint
	input.TemplateDraft.Sections[0].Elements[0].Element.Content = `{"protocol":"blackhole","tag":"direct","another":true}`
	if got, err := service.Publish(t.Context(), request); got != nil || !errors.Is(err, ErrConflict) || len(store.commits) != 0 {
		t.Fatalf("changed held candidate %+v %v", got, err)
	}
	input.TemplateDraft.Sections[0].Elements[0].Element.Content = `{"protocol":"blackhole","tag":"direct"}`
	if got, err := service.Publish(t.Context(), request); err != nil || len(got.Routers) != 2 || len(store.commits) != 1 || checks != 6 {
		t.Fatalf("confirmation %+v %v, validation=%d", got, err, checks)
	}
}

func TestSharedPreviewHasNoPartialCandidateOrWrite(t *testing.T) {
	input, request := sharedFixture(t, false)
	store := &groupStoreStub{inputs: input}
	checks := 0
	service := NewTemplateGroupService(store, catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error {
		checks++
		if checks == 2 {
			return ErrCatalogRejected
		}
		return nil
	}))
	if result, err := service.Preview(t.Context(), request.TemplateID, 1); result != nil || !errors.Is(err, ErrCatalogRejected) || len(store.commits) != 0 {
		t.Fatalf("partial preview %+v %v", result, err)
	}
	input.Members = nil
	if result, err := service.Preview(t.Context(), request.TemplateID, 1); err != nil || len(result.Members) != 0 || len(store.commits) != 0 {
		t.Fatalf("empty preview %+v %v", result, err)
	}
}

func TestGroupChangeEstimatesOnlyActualRules(t *testing.T) {
	file := func(source string) configuration.CatalogFile {
		return configuration.CatalogFile{Name: "05_routing.json", Content: json.RawMessage(source)}
	}
	old := []configuration.CatalogFile{file(`{"routing":{"domainStrategy":"AsIs","rules":[]}}`)}
	for _, row := range []struct {
		next string
		want string
	}{
		{`{"routing":{"domainStrategy":"AsIs","rules":[{"outboundTag":"example"}]}}`, "hot_rules_possible"},
		{`{"routing":{"domainStrategy":"IPIfNonMatch","rules":[{"outboundTag":"example"}]}}`, "restart_expected"},
		{`{ "routing":{ "domainStrategy":"AsIs", "rules":[] } }`, "restart_expected"},
		{`{"routing":{"domainStrategy":"AsIs","rules":[]}}`, "unchanged"},
	} {
		_, got := catalogFileChanges(old, []configuration.CatalogFile{file(row.next)})
		if got != row.want {
			t.Fatalf("estimate %q: %s", row.next, got)
		}
	}
}

func TestSharedPreviewBlocksUnsupportedG08Bypass(t *testing.T) {
	input, request := sharedFixture(t, false)
	input.Exclusions = &configuration.ExclusionSource{Text: "192.0.2.0/24\n"}
	store := &groupStoreStub{inputs: input}
	checks := 0
	service := NewTemplateGroupService(store, catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error {
		checks++
		return nil
	}))
	if p, err := service.Preview(t.Context(), request.TemplateID, request.TemplateDraftVersion); p != nil || !errors.Is(err, ErrBypassUnavailable) {
		t.Fatalf("unsupported preview %v %v", p, err)
	}
	if result, err := service.Publish(t.Context(), request); result != nil || !errors.Is(err, ErrBypassUnavailable) || len(store.commits) != 0 || checks != 0 {
		t.Fatalf("unsupported confirmation %v %v, validated=%d", result, err, checks)
	}
	input.Members = nil
	if p, err := service.Preview(t.Context(), request.TemplateID, request.TemplateDraftVersion); p == nil || err != nil || len(p.Members) != 0 {
		t.Fatalf("empty membership preview %v %v", p, err)
	}
}

func TestSharedPreviewUsesEffectivePersonalExclusions(t *testing.T) {
	input, request := sharedFixture(t, false)
	input.Exclusions = &configuration.ExclusionSource{Text: "192.0.2.0/24\n", ElementID: firstID}
	empty := "# A deliberate empty personal list\n"
	for i := range input.Members {
		input.Members[i].Active.Draft.Exclusion = &routersettings.ExclusionOverride{
			Source: configuration.ElementSource{TemplateID: request.TemplateID, ElementID: firstID}, Replacement: &empty,
		}
	}
	store := &groupStoreStub{inputs: input}
	service := NewTemplateGroupService(store, catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error { return nil }))
	if p, err := service.Preview(t.Context(), request.TemplateID, request.TemplateDraftVersion); err != nil || p == nil || len(p.Members) != 2 || len(store.commits) != 0 {
		t.Fatalf("effective empty exclusions %+v %v", p, err)
	}
	input.Members[len(input.Members)-1].Active.Draft.Exclusion = nil
	if p, err := service.Preview(t.Context(), request.TemplateID, request.TemplateDraftVersion); p != nil || !errors.Is(err, ErrBypassUnavailable) || len(store.commits) != 0 {
		t.Fatalf("affected inheritor must block the full preview %+v %v", p, err)
	}
}
