package configuration_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	c "github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

const (
	outboundRuleA = "550e8400-e29b-41d4-a716-446655440201"
	outboundRuleB = "550e8400-e29b-41d4-a716-446655440202"
	outboundA     = "550e8400-e29b-41d4-a716-446655440203"
	routingA      = "550e8400-e29b-41d4-a716-446655440204"
)

func outboundSnapshot() c.OutboundSnapshot {
	template := c.Owner{Kind: "template", ID: "sample-template"}
	router := c.Owner{Kind: "router", ID: "sample-router"}
	return c.OutboundSnapshot{
		Rules: c.RouterSelectionInput{Template: template, Router: router,
			Shared: []c.SharedElement{{Element: c.Element{ID: outboundRuleA, Content: `{"outboundTag":"${target}"}`, Variables: []c.VariableDefinition{{Name: "target", Type: "string"}}}, Position: "/routing/rules/0"}},
			Local:  []c.LocalContent{{ID: outboundRuleB, Content: json.RawMessage(`{}`), Position: "/local/rules/0"}}},
		Outbounds: c.RouterSelectionInput{Template: template, Router: router,
			Shared: []c.SharedElement{{Element: c.Element{ID: outboundA, Content: `{"tag":"direct"}`}, Position: "/outbounds/0"}}},
		Routing: c.RouterSelectionInput{Template: template, Router: router,
			Shared: []c.SharedElement{{Element: c.Element{ID: routingA, Content: `{"balancers":[]}`}, Position: "/routing"}}},
		TemplateOrder: []string{outboundRuleA}, PersonalOrder: []string{outboundRuleA, outboundRuleB},
		DisabledRules: []c.ElementRef{{ID: outboundRuleA, Position: "/disabledRules/0"}},
	}
}

func TestOutboundDisabledRuleKeepsFullIdentityWithoutContent(t *testing.T) {
	in := outboundSnapshot()
	in.Rules.Shared[0].Element.Content = "{"
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || !got.Ready || len(got.FullRules) != 2 || len(got.References) != 0 {
		t.Fatalf("wrong active projection: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundInactiveIdentityAndOrderAreStillValidated(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*c.OutboundSnapshot)
		code string
	}{
		{"duplicate", func(in *c.OutboundSnapshot) { in.Rules.Local[0].ID = outboundRuleA }, "duplicate_element_id"},
		{"cross-section", func(in *c.OutboundSnapshot) { in.Outbounds.Shared[0].Element.ID = outboundRuleA }, "duplicate_element_id"},
		{"order", func(in *c.OutboundSnapshot) { in.PersonalOrder = []string{outboundRuleB} }, "order_element_not_found"},
		{"routing-source", func(in *c.OutboundSnapshot) {
			in.Routing.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{Source: c.ElementSource{TemplateID: in.Routing.Template.ID, ElementID: outboundA}, Mode: c.ContentModeVariables}, Position: "/routing/overrides/0"}}
		}, "source_not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := outboundSnapshot()
			tc.edit(&in)
			got, issues := c.AnalyzeOutboundDependencies(in)
			if got != nil || len(issues) == 0 || issues[0].Code != tc.code {
				t.Fatalf("expected %s: result=%+v issues=%+v", tc.code, got, issues)
			}
		})
	}
}

func TestOutboundInactiveSourcesAreNotAssembled(t *testing.T) {
	in := outboundSnapshot()
	in.Rules.Shared[0].Element.Content = "{"
	in.Rules.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: in.Rules.Template.ID, ElementID: outboundRuleA}, Position: "/exclusions/0"}}
	in.Outbounds.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: in.Outbounds.Template.ID, ElementID: outboundA}, Position: "/outboundExclusions/0"}}
	in.Outbounds.Shared[0].Element.Content = "{"
	before := outboundSnapshot()
	before.Rules.Shared[0].Element.Content = "{"
	before.Rules.Exclusions = append([]c.SharedExclusion(nil), in.Rules.Exclusions...)
	before.Outbounds.Exclusions = append([]c.SharedExclusion(nil), in.Outbounds.Exclusions...)
	before.Outbounds.Shared[0].Element.Content = "{"
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || !got.Ready || len(got.FullRules) != 2 || len(got.References) != 0 || !reflect.DeepEqual(in, before) {
		t.Fatalf("inactive content was assembled or input changed: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundActiveReplacementSkipsRawTemplate(t *testing.T) {
	in := outboundSnapshot()
	in.DisabledRules = nil
	in.Rules.Shared[0].Element.Content = "{"
	in.Rules.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{
		Source: c.ElementSource{TemplateID: in.Rules.Template.ID, ElementID: outboundRuleA}, Mode: c.ContentModeReplacement,
		Content: json.RawMessage(`{}`),
	}, Position: "/overrides/0"}}
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || !got.Ready || len(got.References) != 0 {
		t.Fatalf("replacement not selected: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundActiveInheritanceAndLocalJSONAreSelected(t *testing.T) {
	in := outboundSnapshot()
	in.DisabledRules = nil
	in.Rules.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{
		Source: c.ElementSource{TemplateID: in.Rules.Template.ID, ElementID: outboundRuleA}, Mode: c.ContentModeVariables,
		Values: map[string]json.RawMessage{"target": json.RawMessage(`"direct"`)},
	}, Position: "/overrides/0"}}
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || !got.Ready {
		t.Fatalf("active inheritance failed: result=%+v issues=%+v", got, issues)
	}
	in.Rules.Local[0].Content = json.RawMessage(`{`)
	got, issues = c.AnalyzeOutboundDependencies(in)
	if got != nil || len(issues) == 0 || issues[0].Code != "invalid_json" || issues[0].ElementID != outboundRuleB {
		t.Fatalf("active local JSON not checked: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundDirectReferencesUseEffectiveRootJSON(t *testing.T) {
	in := outboundSnapshot()
	in.DisabledRules = nil
	in.Rules.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{
		Source: c.ElementSource{TemplateID: in.Rules.Template.ID, ElementID: outboundRuleA}, Mode: c.ContentModeVariables,
		Values: map[string]json.RawMessage{"target": json.RawMessage(`"direct"`)},
	}, Position: "/overrides/0"}}
	in.Rules.Local[0].Content = json.RawMessage(`{"outboundTag":"direct","enabled":false,"extension":{"outboundTag":"ghost"},"ruleTag":"ghost"}`)
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || !got.Ready || len(got.References) != 2 {
		t.Fatalf("effective direct links: result=%+v issues=%+v", got, issues)
	}
	for _, ref := range got.References {
		if ref.Kind != "direct" || ref.Path != "/outboundTag" || ref.Status != "resolved" || !reflect.DeepEqual(ref.OutboundIDs, []string{outboundA}) {
			t.Fatalf("wrong direct link: %+v", ref)
		}
	}
}

func TestOutboundDirectReferenceUsesFullReplacement(t *testing.T) {
	in := outboundSnapshot()
	in.DisabledRules = nil
	in.Rules.Shared[0].Element.Content = "{"
	in.Rules.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{
		Source: c.ElementSource{TemplateID: in.Rules.Template.ID, ElementID: outboundRuleA}, Mode: c.ContentModeReplacement,
		Content: json.RawMessage(`{"outboundTag":"direct"}`),
	}, Position: "/overrides/0"}}
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || !got.Ready || len(got.References) != 1 || got.References[0].RuleID != outboundRuleA || got.References[0].Status != "resolved" {
		t.Fatalf("full replacement direct reference: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundDirectPrecedenceAndInvalidNonselectedField(t *testing.T) {
	in := outboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"outboundTag":"direct","balancerTag":"missing"}`)
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || !got.Ready || len(got.References) != 1 || got.References[0].Kind != "direct" {
		t.Fatalf("direct precedence: result=%+v issues=%+v", got, issues)
	}
	in.Rules.Local[0].Content = json.RawMessage(`{"outboundTag":"direct","balancerTag":42}`)
	got, issues = c.AnalyzeOutboundDependencies(in)
	if got != nil || len(issues) == 0 || issues[0].Code != "invalid_balancer_tag" || issues[0].Path == nil || *issues[0].Path != "/balancerTag" {
		t.Fatalf("nonselected field type: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundMissingAndExcludedOutputAreDraftFindings(t *testing.T) {
	in := outboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"outboundTag":"missing"}`)
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || got.Ready || len(got.References) != 1 || got.References[0].Status != "missing" || got.References[0].Path != "/outboundTag" || got.References[0].RuleID != outboundRuleB {
		t.Fatalf("missing output: result=%+v issues=%+v", got, issues)
	}
	in.Rules.Local[0].Content = json.RawMessage(`{"outboundTag":"direct"}`)
	in.Outbounds.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: in.Outbounds.Template.ID, ElementID: outboundA}, Position: "/outboundExclusions/0"}}
	got, issues = c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || got.Ready || len(got.References) != 1 || got.References[0].Status != "missing" {
		t.Fatalf("excluded output: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundDuplicateActiveTagIsAmbiguous(t *testing.T) {
	in := outboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"outboundTag":"direct"}`)
	in.Outbounds.Local = []c.LocalContent{{ID: "550e8400-e29b-41d4-a716-446655440205", Content: json.RawMessage(`{"tag":"direct"}`), Position: "/local/outbounds/0"}}
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got != nil || len(issues) < 2 || issues[0].Code != "ambiguous_outbound_tag" || issues[0].OtherLocation == nil || issues[0].Location.Position != "/local/outbounds/0" || issues[0].OtherLocation.Position != "/outbounds/0" || issues[1].Code != "ambiguous_outbound_reference" || issues[1].Location.Position != "/local/rules/0" {
		t.Fatalf("ambiguous output: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundInvalidRuleAndOutputHaveSafeErrors(t *testing.T) {
	in := outboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`null`)
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got != nil || len(issues) == 0 || issues[0].Code != "invalid_rule_shape" || issues[0].Path == nil || *issues[0].Path != "" {
		t.Fatalf("invalid rule: result=%+v issues=%+v", got, issues)
	}
	in.Rules.Local[0].Content = json.RawMessage(`{"outboundTag":"direct"}`)
	in.Outbounds.Shared[0].Element.Content = `{"tag":42,"secret":"value-to-hide"}`
	got, issues = c.AnalyzeOutboundDependencies(in)
	if got != nil || len(issues) == 0 || issues[0].Code != "invalid_outbound_tag" || issues[0].Path == nil || *issues[0].Path != "/tag" || strings.Contains(fmt.Sprintf("%+v", issues), "value-to-hide") {
		t.Fatalf("invalid output or leaked payload: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundDirectResultsAreIndependentAndDoNotExposeJSON(t *testing.T) {
	in := outboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"outboundTag":"direct","secret":"value-to-hide"}`)
	before := outboundSnapshot()
	before.Rules.Local[0].Content = append(json.RawMessage(nil), in.Rules.Local[0].Content...)
	first, issues := c.AnalyzeOutboundDependencies(in)
	if first == nil || len(issues) != 0 || len(first.References) != 1 {
		t.Fatalf("analysis failed: result=%+v issues=%+v", first, issues)
	}
	if text := fmt.Sprintf("%+v", first); strings.Contains(text, "value-to-hide") || strings.Contains(text, `"secret"`) {
		t.Fatalf("result exposes selected JSON or tag: %s", text)
	}
	first.FullRules[0].Source.TemplateID = "changed"
	first.References[0].OutboundIDs[0] = "changed"
	second, issues := c.AnalyzeOutboundDependencies(in)
	if second == nil || len(issues) != 0 || second.FullRules[0].Source.TemplateID != in.Rules.Template.ID || second.References[0].OutboundIDs[0] != outboundA || !reflect.DeepEqual(in, before) {
		t.Fatalf("result aliases input or previous result: result=%+v issues=%+v", second, issues)
	}
}

func TestOutboundBalancerReferenceSelectsNestedRouting(t *testing.T) {
	in := outboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"outboundTag":"","balancerTag":"pool"}`)
	in.Routing.Shared[0].Element.Content = `{"balancers":[{"tag":"pool","selector":["direct"]}]}`
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || !got.Ready || len(got.References) < 1 || got.References[0].Kind != "balancer" || got.References[0].Status != "resolved" || got.References[0].RuleID != outboundRuleB {
		t.Fatalf("reachable balancer: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundMissingBalancerIsAnUnreadyDraft(t *testing.T) {
	in := outboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"balancerTag":"missing"}`)
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || got.Ready || len(got.References) != 1 || got.References[0].Kind != "balancer" || got.References[0].Status != "missing" || got.References[0].Path != "/balancerTag" {
		t.Fatalf("missing balancer: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundDuplicateReachableBalancerTagIsAmbiguous(t *testing.T) {
	in := outboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"balancerTag":"pool"}`)
	in.Routing.Shared[0].Element.Content = `{"balancers":[{"tag":"pool","selector":["direct"]},{"tag":"pool","selector":["direct"]}]}`
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got != nil || len(issues) < 3 || issues[0].Code != "ambiguous_balancer_tag" || issues[0].ElementID != routingA || issues[0].Path == nil || *issues[0].Path != "/balancers/0/tag" || issues[1].Path == nil || *issues[1].Path != "/balancers/1/tag" || issues[2].Code != "ambiguous_balancer_reference" || issues[2].ElementID != outboundRuleB || issues[2].Path == nil || *issues[2].Path != "/balancerTag" {
		t.Fatalf("duplicate reachable balancer: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundDirectRuleDoesNotRequireUnreachableRoutingContent(t *testing.T) {
	in := outboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"outboundTag":"direct"}`)
	in.Routing.Shared[0].Element.Content = "{"
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || !got.Ready || len(got.References) != 1 {
		t.Fatalf("unreachable routing was assembled: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundBalancerUsesRoutingReplacement(t *testing.T) {
	in := outboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"balancerTag":"pool"}`)
	in.Routing.Shared[0].Element.Content = "{"
	in.Routing.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{
		Source: c.ElementSource{TemplateID: in.Routing.Template.ID, ElementID: routingA}, Mode: c.ContentModeReplacement,
		Content: json.RawMessage(`{"balancers":[{"tag":"pool","selector":["direct"]}]}`),
	}, Position: "/routingOverride/0"}}
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || !got.Ready || len(got.References) < 1 || got.References[0].Status != "resolved" {
		t.Fatalf("routing replacement: result=%+v issues=%+v", got, issues)
	}
}

func balancerSnapshot(content string) c.OutboundSnapshot {
	in := outboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"balancerTag":"pool"}`)
	in.Routing.Shared[0].Element.Content = content
	return in
}

func TestOutboundSelectorMatchesAllActivePrefixes(t *testing.T) {
	in := balancerSnapshot(`{"balancers":[{"tag":"pool","selector":["edge-"]}]}`)
	in.Outbounds.Shared[0].Element.Content = `{"tag":"edge-a"}`
	in.Outbounds.Local = []c.LocalContent{
		{ID: "550e8400-e29b-41d4-a716-446655440205", Content: json.RawMessage(`{"tag":"edge-b"}`), Position: "/local/outbounds/0"},
		{ID: "550e8400-e29b-41d4-a716-446655440206", Content: json.RawMessage(`{"tag":"direct"}`), Position: "/local/outbounds/1"},
	}
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || !got.Ready || len(got.References) != 2 {
		t.Fatalf("selector result=%+v issues=%+v", got, issues)
	}
	selector := got.References[1]
	if selector.Kind != "selector" || selector.Path != "/balancers/0/selector/0" || selector.ElementID != routingA || selector.Location.Position != "/routing" || selector.Status != "resolved" || !reflect.DeepEqual(selector.OutboundIDs, []string{outboundA, "550e8400-e29b-41d4-a716-446655440205"}) {
		t.Fatalf("selector candidates: %+v", selector)
	}
}

func TestOutboundStringSelectorSplitsWithoutTrim(t *testing.T) {
	in := balancerSnapshot(`{"balancers":[{"tag":"pool","selector":"edge-, backup-"}]}`)
	in.Outbounds.Shared[0].Element.Content = `{"tag":"edge-a"}`
	in.Outbounds.Local = []c.LocalContent{
		{ID: "550e8400-e29b-41d4-a716-446655440205", Content: json.RawMessage(`{"tag":"backup-a"}`), Position: "/local/outbounds/0"},
		{ID: "550e8400-e29b-41d4-a716-446655440206", Content: json.RawMessage(`{"tag":" backup-a"}`), Position: "/local/outbounds/1"},
	}
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || !got.Ready || len(got.References) != 3 {
		t.Fatalf("string selector result=%+v issues=%+v", got, issues)
	}
	if got.References[1].Path != "/balancers/0/selector" || got.References[1].PartIndex != 0 || !reflect.DeepEqual(got.References[1].OutboundIDs, []string{outboundA}) || got.References[2].PartIndex != 1 || !reflect.DeepEqual(got.References[2].OutboundIDs, []string{"550e8400-e29b-41d4-a716-446655440206"}) {
		t.Fatalf("selector string parts: %+v", got.References)
	}
}

func TestOutboundNoSelectorMatchesUsesResolvedFallback(t *testing.T) {
	in := balancerSnapshot(`{"balancers":[{"tag":"pool","selector":["edge-"],"fallbackTag":"direct"}]}`)
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || !got.Ready || len(got.References) != 3 || got.References[1].Status != "no_matches" || got.References[2].Kind != "fallback" || got.References[2].Status != "resolved" || !reflect.DeepEqual(got.References[2].OutboundIDs, []string{outboundA}) {
		t.Fatalf("fallback readiness: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundNoSelectorMatchesWithoutFallbackIsUnready(t *testing.T) {
	in := balancerSnapshot(`{"balancers":[{"tag":"pool","selector":["edge-"]}]}`)
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || got.Ready || len(got.References) != 2 || got.References[1].Status != "no_matches" {
		t.Fatalf("missing fallback: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundMissingFallbackIsUnreadyDespiteSelectorMatches(t *testing.T) {
	in := balancerSnapshot(`{"balancers":[{"tag":"pool","selector":["direct"],"fallbackTag":"missing"}]}`)
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || got.Ready || len(got.References) != 3 || got.References[1].Status != "resolved" || got.References[2].Status != "missing" || got.References[2].Path != "/balancers/0/fallbackTag" {
		t.Fatalf("missing fallback despite matches: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundReachableBalancerShapeErrorsAndUnreachableIsIgnored(t *testing.T) {
	for _, tc := range []struct{ name, content, path string }{
		{"object-selector", `{"balancers":[{"tag":"pool","selector":{}}]}`, "/balancers/0/selector"},
		{"empty-selector", `{"balancers":[{"tag":"pool","selector":[]}]}`, "/balancers/0/selector"},
		{"fallback-type", `{"balancers":[{"tag":"pool","selector":["direct"],"fallbackTag":42}]}`, "/balancers/0/fallbackTag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := balancerSnapshot(tc.content)
			got, issues := c.AnalyzeOutboundDependencies(in)
			if got != nil || len(issues) == 0 || issues[0].ElementID != routingA || issues[0].Path == nil || *issues[0].Path != tc.path {
				t.Fatalf("reachable error: result=%+v issues=%+v", got, issues)
			}
			in.Rules.Local[0].Content = json.RawMessage(`{"outboundTag":"direct"}`)
			got, issues = c.AnalyzeOutboundDependencies(in)
			if got == nil || len(issues) != 0 || !got.Ready {
				t.Fatalf("unreachable balancer was checked: result=%+v issues=%+v", got, issues)
			}
		})
	}
}

func TestOutboundSelectorRecalculatesAfterProviderChange(t *testing.T) {
	in := balancerSnapshot(`{"balancers":[{"tag":"pool","selector":["edge-"]}]}`)
	in.Outbounds.Shared[0].Element.Content = `{"tag":"edge-a"}`
	in.Outbounds.Local = []c.LocalContent{{ID: "550e8400-e29b-41d4-a716-446655440205", Content: json.RawMessage(`{"tag":"edge-b"}`), Position: "/local/outbounds/0"}}
	first, issues := c.AnalyzeOutboundDependencies(in)
	if first == nil || len(issues) != 0 || len(first.References) < 2 || !reflect.DeepEqual(first.References[1].OutboundIDs, []string{outboundA, "550e8400-e29b-41d4-a716-446655440205"}) {
		t.Fatalf("initial candidates: result=%+v issues=%+v", first, issues)
	}
	in.Outbounds.Shared[0].Element.Content = `{"tag":"renamed"}`
	second, issues := c.AnalyzeOutboundDependencies(in)
	if second == nil || len(issues) != 0 || !second.Ready || len(second.References) < 2 || !reflect.DeepEqual(second.References[1].OutboundIDs, []string{"550e8400-e29b-41d4-a716-446655440205"}) {
		t.Fatalf("recalculated candidates: result=%+v issues=%+v", second, issues)
	}
}

func TestOutboundDuplicateTagThroughBalancerReportsNestedAndRuleLocations(t *testing.T) {
	in := balancerSnapshot(`{"balancers":[{"tag":"pool","selector":["edge-"],"fallbackTag":"edge-a"}]}`)
	in.Outbounds.Shared[0].Element.Content = `{"tag":"edge-a"}`
	in.Outbounds.Local = []c.LocalContent{{ID: "550e8400-e29b-41d4-a716-446655440205", Content: json.RawMessage(`{"tag":"edge-a"}`), Position: "/local/outbounds/0"}}
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got != nil || len(issues) < 4 || issues[0].Code != "ambiguous_outbound_tag" || issues[0].OtherLocation == nil {
		t.Fatalf("duplicate output through balancer: result=%+v issues=%+v", got, issues)
	}
	var selector, fallback, rule bool
	for _, issue := range issues[1:] {
		if issue.Path == nil {
			continue
		}
		switch *issue.Path {
		case "/balancers/0/selector/0":
			selector = issue.ElementID == routingA && issue.Location.Position == "/routing"
		case "/balancers/0/fallbackTag":
			fallback = issue.ElementID == routingA && issue.Location.Position == "/routing"
		case "/balancerTag":
			rule = issue.ElementID == outboundRuleB && issue.Location.Position == "/local/rules/0"
		}
	}
	if !selector || !fallback || !rule {
		t.Fatalf("missing related locations: %+v", issues)
	}
}

func TestOutboundMissingFallbackWithNoSelectorMatchesReportsBothPlaces(t *testing.T) {
	in := balancerSnapshot(`{"balancers":[{"tag":"pool","selector":["edge-"],"fallbackTag":"missing"}]}`)
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || got.Ready || len(got.References) != 3 || got.References[1].Status != "no_matches" || got.References[1].Path != "/balancers/0/selector/0" || got.References[2].Status != "missing" || got.References[2].Path != "/balancers/0/fallbackTag" || got.References[2].ElementID != routingA || got.References[2].Location.Position != "/routing" {
		t.Fatalf("both missing paths: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundNestedKeyAloneDoesNotCreateReference(t *testing.T) {
	in := outboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"extension":{"outboundTag":"ghost"},"ruleTag":"diagnostic"}`)
	got, issues := c.AnalyzeOutboundDependencies(in)
	if got == nil || len(issues) != 0 || !got.Ready || len(got.References) != 0 {
		t.Fatalf("nested key became a reference: result=%+v issues=%+v", got, issues)
	}
}

func TestOutboundNestedResultsAreIndependentAndSafe(t *testing.T) {
	in := balancerSnapshot(`{"balancers":[{"tag":"pool","selector":["direct"],"fallbackTag":"direct","secret":"value-to-hide"}]}`)
	before := balancerSnapshot(in.Routing.Shared[0].Element.Content)
	first, issues := c.AnalyzeOutboundDependencies(in)
	if first == nil || len(issues) != 0 || len(first.References) != 3 {
		t.Fatalf("nested result: result=%+v issues=%+v", first, issues)
	}
	if text := fmt.Sprintf("%+v", first); strings.Contains(text, "value-to-hide") || strings.Contains(text, `"secret"`) {
		t.Fatalf("nested result leaks JSON: %s", text)
	}
	first.References[1].OutboundIDs[0] = "changed"
	first.References[2].Status = "changed"
	second, issues := c.AnalyzeOutboundDependencies(in)
	if second == nil || len(issues) != 0 || second.References[1].OutboundIDs[0] != outboundA || second.References[2].Status != "resolved" || !reflect.DeepEqual(in, before) {
		t.Fatalf("nested alias or input mutation: result=%+v issues=%+v", second, issues)
	}
}
