package publication

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

const (
	firstID  = "550e8400-e29b-41d4-a716-446655440001"
	secondID = "550e8400-e29b-41d4-a716-446655440002"
	thirdID  = "550e8400-e29b-41d4-a716-446655440003"
	fourthID = "550e8400-e29b-41d4-a716-446655440004"
	fifthID  = "550e8400-e29b-41d4-a716-446655440005"
	sixthID  = "550e8400-e29b-41d4-a716-446655440006"
)

func snapshots() (*templates.Snapshot, *routersettings.Draft) {
	return &templates.Snapshot{TemplateDraft: configuration.TemplateDraft{ID: "template-demo"}, Version: 2},
		&routersettings.Draft{ID: "router.example", TemplateID: "template-demo"}
}

func TestPrepareRouterCandidateEmptyCompleteSnapshots(t *testing.T) {
	template, router := snapshots()
	got, issues := PrepareRouterCandidate(template, router, nil)
	if len(issues) != 0 || got == nil || got.Catalog == nil || got.TemplateID != template.ID ||
		got.TemplateVersion != template.Version || got.RouterID != router.ID {
		t.Fatalf("prepare = %#v, %#v", got, issues)
	}
	want := []configuration.CatalogFile{
		{Name: "01_log.json", Content: json.RawMessage("{\n  \"log\": {}\n}\n")},
		{Name: "02_api.json", Content: json.RawMessage("{}\n")},
		{Name: "03_inbounds.json", Content: json.RawMessage("{\n  \"inbounds\": []\n}\n")},
		{Name: "04_outbounds.json", Content: json.RawMessage("{\n  \"outbounds\": []\n}\n")},
		{Name: "05_routing.json", Content: json.RawMessage("{\n  \"routing\": {\n    \"rules\": []\n  }\n}\n")},
	}
	if !reflect.DeepEqual(got.Catalog.Files, want) {
		t.Errorf("base files = %#v", got.Catalog.Files)
	}
	if got.PersonalOrder != nil || got.OrderBasis != nil {
		t.Errorf("inherited order returned a personal basis: %#v", got)
	}
}

func TestPrepareRouterCandidateRejectsInvalidContext(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(**templates.Snapshot, **routersettings.Draft)
		code   string
	}{
		{"missing template", func(template **templates.Snapshot, _ **routersettings.Draft) { *template = nil }, "missing_publication_snapshot"},
		{"missing router", func(_ **templates.Snapshot, router **routersettings.Draft) { *router = nil }, "missing_publication_snapshot"},
		{"wrong template", func(_ **templates.Snapshot, router **routersettings.Draft) { (*router).TemplateID = "other" }, "invalid_publication_context"},
		{"invalid ID", func(_ **templates.Snapshot, router **routersettings.Draft) { (*router).ID = "\xffsecret" }, "invalid_publication_context"},
		{"invalid version", func(template **templates.Snapshot, _ **routersettings.Draft) { (*template).Version = 0 }, "invalid_publication_context"},
		{"template group duplicate", func(template **templates.Snapshot, _ **routersettings.Draft) {
			(*template).Sections = []configuration.TemplateSection{{Name: "rules"}, {Name: "rules"}}
		}, "duplicate_catalog_section"},
		{"router group duplicate", func(_ **templates.Snapshot, router **routersettings.Draft) {
			(*router).Groups = []routersettings.Group{{Name: "outbounds"}, {Name: "outbounds"}}
		}, "duplicate_catalog_section"},
	} {
		t.Run(test.name, func(t *testing.T) {
			template, router := snapshots()
			test.mutate(&template, &router)
			got, issues := PrepareRouterCandidate(template, router, nil)
			requireCandidateIssue(t, got, issues, test.code)
			if test.code == "invalid_publication_context" && issues[0].Location.Owner.ID != "" {
				t.Errorf("context error echoes a supplied ID: %#v", issues)
			}
		})
	}
}

func TestPrepareRouterCandidatePreservesB12Semantics(t *testing.T) {
	template, router := snapshots()
	template.Sections = []configuration.TemplateSection{
		{Name: "inbounds", Elements: []configuration.TemplateElement{
			{Disabled: true, Element: configuration.Element{ID: firstID, Content: `"${notReady}"`}},
		}},
		{Name: "outbounds", Elements: []configuration.TemplateElement{
			{Element: configuration.Element{ID: secondID, Content: `{"protocol":"${protocol}","tag":"shared"}`,
				Variables: []configuration.VariableDefinition{{Name: "protocol", Type: "string"}}}},
			{Element: configuration.Element{ID: thirdID, Content: `"${notReady}"`}},
			{Element: configuration.Element{ID: fourthID, Content: `"${notReady}"`}},
		}},
	}
	router.DisabledInbounds = []string{firstID}
	router.Groups = []routersettings.Group{
		{Name: "outbounds",
			Local: []routersettings.LocalElement{{ID: fifthID, Content: json.RawMessage(`{"protocol":"freedom","tag":"local"}`)}},
			Overrides: []configuration.ContentOverride{
				{Source: configuration.ElementSource{TemplateID: template.ID, ElementID: secondID},
					Mode: configuration.ContentModeVariables, Values: map[string]json.RawMessage{"protocol": json.RawMessage(`"freedom"`)}},
				{Source: configuration.ElementSource{TemplateID: template.ID, ElementID: thirdID},
					Mode: configuration.ContentModeReplacement, Content: json.RawMessage(`{"protocol":"blackhole","tag":"replacement","literal":"${literal}"}`)},
			},
			Exclusions: []configuration.ElementSource{{TemplateID: template.ID, ElementID: fourthID}},
		},
		{Name: "stats"},
		{Name: "future", Local: []routersettings.LocalElement{
			{ID: sixthID, Content: json.RawMessage(`[9007199254740993,false,null]`)},
		}},
	}
	got, issues := PrepareRouterCandidate(template, router, nil)
	if got == nil || got.Catalog == nil || len(issues) != 0 || len(got.Catalog.Files) != 6 {
		t.Fatalf("prepare = %#v, %#v", got, issues)
	}
	files := candidateFiles(got.Catalog)
	want := map[string]string{
		"02_api.json":      "{\n  \"stats\": {}\n}\n",
		"03_inbounds.json": "{\n  \"inbounds\": []\n}\n",
		"04_outbounds.json": `{
  "outbounds": [
    {
      "_xkeenUiId": "550e8400-e29b-41d4-a716-446655440002",
      "protocol": "freedom",
      "tag": "shared"
    },
    {
      "_xkeenUiId": "550e8400-e29b-41d4-a716-446655440003",
      "literal": "${literal}",
      "protocol": "blackhole",
      "tag": "replacement"
    },
    {
      "_xkeenUiId": "550e8400-e29b-41d4-a716-446655440005",
      "protocol": "freedom",
      "tag": "local"
    }
  ]
}
`,
		"06_extra.json": "{\n  \"future\": [\n    9007199254740993,\n    false,\n    null\n  ]\n}\n",
	}
	for name, content := range want {
		if files[name] != content {
			t.Errorf("%s = %s; want %s", name, files[name], content)
		}
	}
	found := false
	for _, element := range got.Catalog.Elements {
		if element.ElementID == fifthID && element.Location.Owner.ID == router.ID &&
			element.Location.Position == "/groups/0/local/0" && element.Path == "/outbounds/2" {
			found = true
		}
	}
	if !found {
		t.Errorf("local provenance absent: %#v", got.Catalog.Elements)
	}
	router.DisabledInbounds = append(router.DisabledInbounds, firstID)
	got, issues = PrepareRouterCandidate(template, router, nil)
	requireCandidateIssue(t, got, issues, "duplicate_disabled_inbound")
}

func TestPrepareRouterCandidateRejectsUnsupportedDisabledSection(t *testing.T) {
	template, router := snapshots()
	template.Sections = []configuration.TemplateSection{{Name: "outbounds", Elements: []configuration.TemplateElement{
		{Disabled: true, Element: configuration.Element{ID: firstID, Content: `{"protocol":"freedom"}`}},
	}}}
	got, issues := PrepareRouterCandidate(template, router, nil)
	requireCandidateIssue(t, got, issues, "unsupported_disabled_section")
	if issues[0].Location.Owner.ID != template.ID || issues[0].Location.Position != "/sections/0/elements/0" {
		t.Errorf("disable issue has invalid location: %#v", issues)
	}
}

func candidateFiles(catalog *configuration.XrayCatalog) map[string]string {
	result := make(map[string]string, len(catalog.Files))
	for _, file := range catalog.Files {
		result[file.Name] = string(file.Content)
	}
	return result
}

func requireCandidateIssue(t *testing.T, got *PreparedRouterCandidate, issues []configuration.CatalogIssue, want string) {
	t.Helper()
	if got != nil || len(issues) == 0 || issues[0].Code != want {
		t.Fatalf("prepare = %#v, %#v; want %s", got, issues, want)
	}
}

func TestPrepareRouterCandidateRequiresOrderBasis(t *testing.T) {
	template, router := snapshots()
	router.PersonalOrder = []string{}
	for _, test := range []struct {
		name  string
		basis *OrderBasis
		code  string
	}{
		{"missing", nil, "order_basis_required"},
		{"missing full list", &OrderBasis{TemplateID: template.ID, TemplateVersion: template.Version}, "order_basis_required"},
		{"foreign template", &OrderBasis{TemplateID: "different", TemplateVersion: template.Version, SharedRuleIDs: []string{}}, "order_basis_mismatch"},
		{"nonpositive version", &OrderBasis{TemplateID: template.ID, TemplateVersion: 0, SharedRuleIDs: []string{}}, "order_basis_mismatch"},
		{"future version", &OrderBasis{TemplateID: template.ID, TemplateVersion: template.Version + 1, SharedRuleIDs: []string{}}, "order_basis_mismatch"},
		{"current version with a different list", &OrderBasis{TemplateID: template.ID, TemplateVersion: template.Version, SharedRuleIDs: []string{firstID}}, "order_basis_mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, issues := PrepareRouterCandidate(template, router, test.basis)
			requireCandidateIssue(t, got, issues, test.code)
		})
	}
	basis := &OrderBasis{TemplateID: template.ID, TemplateVersion: template.Version, SharedRuleIDs: []string{}}
	got, issues := PrepareRouterCandidate(template, router, basis)
	if got == nil || len(issues) != 0 || got.OrderBasis == basis || got.OrderBasis == nil ||
		got.OrderBasis.SharedRuleIDs == nil || got.PersonalOrder == nil ||
		len(got.OrderBasis.SharedRuleIDs) != 0 || len(got.PersonalOrder) != 0 {
		t.Fatalf("explicit empty order = %#v, %#v", got, issues)
	}
}

func TestPrepareRouterCandidateDoesNotInferLocalOrder(t *testing.T) {
	template, router := snapshots()
	router.Groups = []routersettings.Group{{Name: "rules", Local: []routersettings.LocalElement{
		{ID: firstID, Content: json.RawMessage(`{}`)},
	}}}
	got, issues := PrepareRouterCandidate(template, router, nil)
	requireCandidateIssue(t, got, issues, "order_element_not_found")
}

func TestPrepareRouterCandidateReconcilesBeforeFilters(t *testing.T) {
	template, router := snapshots()
	template.Sections = []configuration.TemplateSection{
		{Name: "rules", Elements: []configuration.TemplateElement{
			{Element: configuration.Element{ID: firstID, Content: `"${notReady}"`}},
			{Element: configuration.Element{ID: fourthID, Content: `{"type":"field","ruleTag":"new","domain":["domain:example.net"],"outboundTag":"direct"}`}},
			{Disabled: true, Element: configuration.Element{ID: secondID, Content: `"${notReady}"`}},
		}},
		{Name: "outbounds", Elements: []configuration.TemplateElement{
			{Element: configuration.Element{ID: sixthID, Content: `{"protocol":"freedom","tag":"direct"}`}},
		}},
	}
	router.Groups = []routersettings.Group{{
		Name:  "rules",
		Local: []routersettings.LocalElement{{ID: fifthID, Content: json.RawMessage(`{"type":"field","ruleTag":"local","domain":["domain:local.example.net"],"outboundTag":"direct"}`)}},
		Exclusions: []configuration.ElementSource{
			{TemplateID: template.ID, ElementID: firstID},
			{TemplateID: template.ID, ElementID: fourthID},
		},
	}}
	router.DisabledRules = []string{secondID}
	router.PersonalOrder = []string{fifthID, firstID, thirdID, secondID}
	basis := &OrderBasis{TemplateID: template.ID, TemplateVersion: 1, SharedRuleIDs: []string{firstID, secondID, thirdID}}
	got, issues := PrepareRouterCandidate(template, router, basis)
	if got == nil || len(issues) != 0 {
		t.Fatalf("reconcile = %#v, %#v", got, issues)
	}
	wantOrder := []string{fifthID, fourthID, firstID, secondID}
	wantBasis := &OrderBasis{TemplateID: template.ID, TemplateVersion: template.Version, SharedRuleIDs: []string{firstID, fourthID, secondID}}
	if !reflect.DeepEqual(got.PersonalOrder, wantOrder) || !reflect.DeepEqual(got.OrderBasis, wantBasis) {
		t.Fatalf("order = %v, basis = %#v; want %v / %#v", got.PersonalOrder, got.OrderBasis, wantOrder, wantBasis)
	}
	const want = `{
  "routing": {
    "rules": [
      {
        "_xkeenUiId": "550e8400-e29b-41d4-a716-446655440005",
        "domain": [
          "domain:local.example.net"
        ],
        "outboundTag": "direct",
        "ruleTag": "local",
        "type": "field"
      }
    ]
  }
}
`
	if actual := candidateFiles(got.Catalog)["05_routing.json"]; actual != want {
		t.Fatalf("visible routing = %s; want %s", actual, want)
	}
	got.PersonalOrder[0] = "changed"
	got.OrderBasis.SharedRuleIDs[0] = "changed"
	got.Catalog.Files[4].Content[0] = 'x'
	again, issues := PrepareRouterCandidate(template, router, basis)
	if again == nil || len(issues) != 0 || !reflect.DeepEqual(again.PersonalOrder, wantOrder) ||
		!reflect.DeepEqual(again.OrderBasis, wantBasis) || candidateFiles(again.Catalog)["05_routing.json"] != want ||
		!reflect.DeepEqual(router.PersonalOrder, []string{fifthID, firstID, thirdID, secondID}) ||
		!reflect.DeepEqual(basis.SharedRuleIDs, []string{firstID, secondID, thirdID}) {
		t.Fatalf("result mutated its inputs or another candidate: %#v, %#v", again, issues)
	}
	router.Groups[0].Exclusions = append(router.Groups[0].Exclusions,
		configuration.ElementSource{TemplateID: template.ID, ElementID: thirdID})
	got, issues = PrepareRouterCandidate(template, router, basis)
	requireCandidateIssue(t, got, issues, "source_not_found")
	if issues[0].Location.Position != "/groups/0/exclusions/2" || len(router.Groups[0].Exclusions) != 3 {
		t.Fatalf("stale source was rewritten or lost its position: %#v", issues)
	}
}
