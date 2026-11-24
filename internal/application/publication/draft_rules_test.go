package publication

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestDraftRulesInsertUsesTheFullOrderAndRetainsUnrelatedData(t *testing.T) {
	template, router := snapshots()
	template.Sections = []configuration.TemplateSection{
		{Name: "rules", Elements: []configuration.TemplateElement{
			{Element: configuration.Element{ID: firstID, Content: `{"ruleTag":"common","type":"field"}`}},
			{Disabled: true, Element: configuration.Element{ID: secondID, Content: `{"ruleTag":"disabled","type":"field"}`}},
		}},
		{Name: "outbounds", Elements: []configuration.TemplateElement{{Element: configuration.Element{ID: thirdID, Content: `{"tag":"other"}`}}}},
	}
	router.Groups = []routersettings.Group{{Name: "rules", Exclusions: []configuration.ElementSource{{TemplateID: template.ID, ElementID: firstID}}}}
	view, err := PrepareDraftRules(template, *router, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) != 2 || view.Entries[0].ID != firstID || !view.Entries[0].Excluded || view.Entries[1].ID != secondID || !view.Entries[1].Disabled {
		t.Fatal("full order did not retain excluded and disabled entries")
	}
	const local = "d21e8a4a-9eb3-4f69-8c31-5dd4d77fdce2"
	raw := json.RawMessage(" {\r\n \"ruleTag\":\"exact\",\"number\":9007199254740993\n}\r\n")
	draft, basis, err := view.Insert(local, firstID, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(draft.PersonalOrder, []string{firstID, local, secondID}) ||
		!reflect.DeepEqual(basis.SharedRuleIDs, []string{firstID, secondID}) ||
		basis.TemplateID != template.ID || basis.TemplateVersion != template.Version ||
		string(draft.Groups[0].Local[0].Content) != string(raw) || len(router.Groups[0].Local) != 0 ||
		!reflect.DeepEqual(draft.Groups[0].Exclusions, router.Groups[0].Exclusions) {
		t.Fatal("insert lost exact content, scoped metadata or the chosen full position")
	}
	for _, existing := range []string{thirdID, secondID} {
		if _, _, err := view.Insert(existing, "", json.RawMessage(`{}`)); !errors.Is(err, ErrInvalidCandidate) {
			t.Fatalf("cross-group ID collision: %v", err)
		}
	}
	const foreign = "a41e8a4a-9eb3-4f69-8c31-5dd4d77fdce3"
	if _, _, err := view.Insert(local, foreign, raw); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown insertion basis: %v", err)
	}
	var sourceError *LocalRuleSourceError
	if _, _, err := view.Insert(local, "", json.RawMessage(`{"x":0,"x":1}`)); !errors.As(err, &sourceError) {
		t.Fatalf("missing JSON error: %v", err)
	}
	if _, err := PrepareDraftRules(template, draft, nil); err == nil {
		t.Fatal("explicit order without evidence was silently accepted")
	}
	next := *template
	next.Version++
	next.Sections[0].Elements = []configuration.TemplateElement{template.Sections[0].Elements[1], template.Sections[0].Elements[0]}
	projected, err := PrepareDraftRules(&next, draft, basis)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{projected.Entries[0].ID, projected.Entries[1].ID, projected.Entries[2].ID}
	if !reflect.DeepEqual(got, []string{firstID, local, secondID}) {
		t.Fatalf("B08 did not preserve the personalized order across a shared reorder: %v", got)
	}
}

func TestEditableRuleOrderBasisOnlyUsesProvenLegacyInputs(t *testing.T) {
	_, draft := snapshots()
	draft.Groups = []routersettings.Group{{Name: "rules", Local: []routersettings.LocalElement{{ID: thirdID, Content: json.RawMessage(`{}`)}}}}
	draft.PersonalOrder = []string{firstID, thirdID}
	current := *draft
	basis := &OrderBasis{TemplateID: draft.TemplateID, TemplateVersion: 1, SharedRuleIDs: []string{firstID}}
	active := &ActiveSettings{Draft: current, OrderBasis: basis}
	legacy := &VersionedRouterDraft{Draft: *draft}
	if EditableRuleOrderBasis(legacy, active) != basis {
		t.Fatal("proved legacy order was rejected")
	}
	legacy.Draft.Groups = nil
	if EditableRuleOrderBasis(legacy, active) != nil {
		t.Fatal("missing local identities were ignored")
	}
	explicit := &OrderBasis{TemplateID: draft.TemplateID, TemplateVersion: 2, SharedRuleIDs: []string{secondID}}
	legacy.OrderBasis = explicit
	if EditableRuleOrderBasis(legacy, active) != explicit {
		t.Fatal("persisted draft evidence should take precedence")
	}
}
