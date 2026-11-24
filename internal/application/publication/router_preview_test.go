package publication

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestPreviewRouterFilesKeepsDesiredAndAssemblesOnlySelectedSource(t *testing.T) {
	accepted, draft := snapshots()
	draft.Groups = []routersettings.Group{{Name: "outbounds", Local: []routersettings.LocalElement{
		{ID: firstID, Content: json.RawMessage(`{"protocol":"freedom","tag":"example-output","Unknown":9007199254740993}`)},
	}}}
	const exact = " {\r\n \"unknown\": 9007199254740993\n}\r\n"
	input := &Inputs{
		RouterID: draft.ID, TemplateID: accepted.ID, Accepted: accepted,
		Active:               &ActiveSettings{Draft: routersettings.Draft{ID: draft.ID, TemplateID: accepted.ID}, Generation: 3},
		Desired:              &Revision{Number: 9, Catalog: VerifiedFiles{Profile: configuration.XrayIdentityVersion, Files: []configuration.CatalogFile{{Name: "04_outbounds.json", Content: json.RawMessage(exact)}}}},
		RouterDraftRequested: true, RouterDraft: &VersionedRouterDraft{Draft: *draft, Generation: 7},
	}
	view, err := PreviewRouterFiles(input, PreviewDesired, nil)
	if err != nil || len(view.Files) != 1 || string(view.Files[0].Content) != exact || view.Expected.RouterDraftGeneration != nil {
		t.Fatalf("desired view: %+v %v", view, err)
	}
	view.Files[0].Content[0] = 'X'
	if string(input.Desired.Catalog.Files[0].Content) != exact {
		t.Fatal("preview exposed aliased desired bytes")
	}
	view, err = PreviewRouterFiles(input, PreviewPersonal, nil)
	if err != nil || len(view.Files) != 5 || view.Expected.RouterDraftGeneration == nil || *view.Expected.RouterDraftGeneration != 7 {
		t.Fatalf("editable preview: %+v %v", view, err)
	}
	if !strings.Contains(string(view.Files[3].Content), `"Unknown": 9007199254740993`) {
		t.Fatalf("lost unknown exact number: %q", view.Files[3].Content)
	}
	changed := *input.RouterDraft
	changed.Draft.TemplateID = "other"
	input.RouterDraft = &changed
	if preview, err := PreviewRouterFiles(input, PreviewPersonal, nil); preview != nil || !errors.Is(err, ErrSourceMismatch) {
		t.Fatalf("foreign preview: %+v %v", preview, err)
	}
}

func TestPreviewRouterFilesNeedsMatchingRuleOrderBasis(t *testing.T) {
	accepted, draft := snapshots()
	accepted.Sections = []configuration.TemplateSection{{Name: "rules", Elements: []configuration.TemplateElement{
		{Element: configuration.Element{ID: firstID, Content: `{"type":"field","outboundTag":"direct","domain":["domain:example.net"]}`}},
	}}, {Name: "outbounds", Elements: []configuration.TemplateElement{{Element: configuration.Element{ID: secondID, Content: `{"protocol":"freedom","tag":"direct"}`}}}}}
	draft.PersonalOrder = []string{firstID}
	basis := &OrderBasis{TemplateID: accepted.ID, TemplateVersion: accepted.Version, SharedRuleIDs: []string{firstID}}
	input := &Inputs{RouterID: draft.ID, TemplateID: accepted.ID, Accepted: accepted,
		Active:               &ActiveSettings{Draft: routersettings.Draft{ID: draft.ID, TemplateID: accepted.ID}, Generation: 1},
		Desired:              &Revision{Number: 1, Catalog: VerifiedFiles{Profile: configuration.XrayIdentityVersion}},
		RouterDraftRequested: true, RouterDraft: &VersionedRouterDraft{Draft: *draft, Generation: 1}}
	preview, err := PreviewRouterFiles(input, PreviewPersonal, nil)
	var diagnostic *PreparationError
	if preview != nil || !errors.As(err, &diagnostic) || len(diagnostic.Issues) == 0 || diagnostic.Issues[0].Code != "order_basis_required" {
		t.Fatalf("unproven basis: %+v %v", preview, err)
	}
	if result, err := PreviewRouterFiles(input, PreviewPersonal, basis); err != nil || result == nil {
		t.Fatalf("draft basis: %+v %v", result, err)
	}
	input.Active.Draft.PersonalOrder = []string{firstID}
	input.Active.OrderBasis = basis
	if result, err := PreviewRouterFiles(input, PreviewPersonal, nil); err != nil || result == nil {
		t.Fatalf("proven legacy basis: %+v %v", result, err)
	}
	input.RouterDraft.Draft.Groups = []routersettings.Group{{Name: "rules", Local: []routersettings.LocalElement{{ID: thirdID, Content: json.RawMessage(`{}`)}}}}
	if result, err := PreviewRouterFiles(input, PreviewPersonal, nil); err == nil || result != nil {
		t.Fatalf("unproven local members: %+v %v", result, err)
	}
}
