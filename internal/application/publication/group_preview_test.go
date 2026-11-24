package publication

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

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
