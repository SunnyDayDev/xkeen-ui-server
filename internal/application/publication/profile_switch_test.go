package publication

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

const switchOutbound = "550e8400-e29b-41d4-a716-446655440083"

type switchStoreStub struct {
	inputs  ProfileSwitchInputs
	commits []ProfileSwitchPlan
}

func (s *switchStoreStub) ReadProfileSwitch(_ context.Context, _, _ string) (*ProfileSwitchInputs, error) {
	out := s.inputs
	return &out, nil
}

func (s *switchStoreStub) CommitProfileSwitch(_ context.Context, p ProfileSwitchPlan) (int64, error) {
	s.commits = append(s.commits, p)
	number := s.inputs.Desired.Number
	if p.NewRevision != nil {
		number++
	}
	return number, nil
}

func profileSwitchFixture(t *testing.T) (*switchStoreStub, ProfileSwitchRequest) {
	t.Helper()
	target := &templates.Snapshot{TemplateDraft: configuration.TemplateDraft{ID: "office", Sections: []configuration.TemplateSection{
		{Name: "outbounds", Elements: []configuration.TemplateElement{{Element: configuration.Element{
			ID: switchOutbound, Content: `{"protocol":"freedom","tag":"${label}"}`,
			Variables: []configuration.VariableDefinition{{Name: "label", Type: "string"}},
		}}}},
	}}, Version: 2}
	active := &ActiveSettings{Draft: routersettings.Draft{ID: "router.example", TemplateID: "home", Name: "router name",
		Groups: []routersettings.Group{{Name: "outbounds", Local: []routersettings.LocalElement{
			{ID: switchOutbound, Content: []byte(`{"protocol":"blackhole","tag":"home-only"}`)},
		}}},
	}, Generation: 7}
	return &switchStoreStub{inputs: ProfileSwitchInputs{
		Basis: ProfileSwitchBasis{RouterID: active.Draft.ID, SourceTemplateID: "home", TargetTemplateID: target.ID,
			SourceAcceptedVersion: 1, TargetAcceptedVersion: 2, ActiveGeneration: active.Generation, DesiredNumber: 8},
		Current: active, Target: target, Desired: &Revision{Number: 8, Catalog: VerifiedFiles{Profile: configuration.XrayIdentityVersion}},
	}}, ProfileSwitchRequest{RouterID: active.Draft.ID, TargetTemplateID: target.ID}
}

func TestProfileSwitchBindsExactEditsAndNeverCopiesSource(t *testing.T) {
	store, request := profileSwitchFixture(t)
	checked := 0
	service := NewProfileSwitchService(store, catalogValidatorFunc(func(_ context.Context, _ string, _ []configuration.CatalogFile) error {
		checked++
		return nil
	}))
	var prep *PreparationError
	if preview, err := service.Preview(t.Context(), request); preview != nil || !errors.As(err, &prep) || len(prep.Issues) != 1 || prep.Issues[0].Code != "missing_value" {
		t.Fatalf("missing required value: preview=%#v, err=%v, issues=%#v", preview, err, prep)
	}
	exact := "\r\n \"direct\" \r\n"
	request.Values = []ProfileVariableEdit{{ElementID: switchOutbound, Name: "label", Source: &exact}}
	preview, err := service.Preview(t.Context(), request)
	if err != nil || preview.Restoring || !preview.Changed || len(preview.Changes) == 0 || checked != 1 || len(store.commits) != 0 {
		t.Fatalf("preview = %#v, %v, checks=%d commits=%d", preview, err, checked, len(store.commits))
	}
	equivalent := `"direct"`
	request.Values[0].Source = &equivalent
	if _, err := service.Confirm(t.Context(), request, preview.Proof); !errors.Is(err, ErrConflict) || len(store.commits) != 0 {
		t.Fatalf("changed literal accepted under old proof: %v", err)
	}
	request.Values[0].Source = &exact
	result, err := service.Confirm(t.Context(), request, preview.Proof)
	if err != nil || result.Revision != 9 || !result.Changed || len(store.commits) != 1 {
		t.Fatalf("checked confirmation = %#v %v", result, err)
	}
	saved := store.commits[0].Settings
	if saved.TemplateID != "office" || saved.Name != "router name" || saved.PersonalOrder != nil || len(saved.Groups) != 1 ||
		len(saved.Groups[0].Local) != 0 || len(saved.Groups[0].Overrides) != 1 ||
		string(saved.Groups[0].Overrides[0].Values["label"]) != exact {
		t.Fatalf("first destination borrowed source or lost bytes: %#v", saved)
	}
}

func TestProfileSwitchRechecksAfterValidatorAndRetainsEquivalentRevision(t *testing.T) {
	store, request := profileSwitchFixture(t)
	exact := `"direct"`
	request.Values = []ProfileVariableEdit{{ElementID: switchOutbound, Name: "label", Source: &exact}}
	reject := true
	late := false
	var validated []configuration.CatalogFile
	service := NewProfileSwitchService(store, catalogValidatorFunc(func(_ context.Context, _ string, files []configuration.CatalogFile) error {
		if reject {
			return ErrCatalogRejected
		}
		validated = slices.Clone(files)
		if late {
			store.inputs.Basis.TargetAcceptedVersion++
			store.inputs.Target.Version++
		}
		return nil
	}))
	if _, err := service.Preview(t.Context(), request); !errors.Is(err, ErrCatalogRejected) || len(store.commits) != 0 {
		t.Fatalf("Xray rejection: %v", err)
	}
	reject, late = false, true
	if _, err := service.Preview(t.Context(), request); !errors.Is(err, ErrConflict) || len(store.commits) != 0 {
		t.Fatalf("late stale preview: %v", err)
	}
	store.inputs.Basis.TargetAcceptedVersion--
	store.inputs.Target.Version--
	late = false
	store.inputs.Desired.Catalog.Files = validated
	view, err := service.Preview(t.Context(), request)
	if err != nil || view.Changed {
		t.Fatalf("equivalent files changed: %#v %v", view, err)
	}
	result, err := service.Confirm(t.Context(), request, view.Proof)
	if err != nil || result.Changed || result.Revision != 8 || len(store.commits) != 1 || store.commits[0].NewRevision != nil {
		t.Fatalf("same-file switch = %#v %v", result, err)
	}
}
