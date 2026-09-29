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
	inboundRuleA = "550e8400-e29b-41d4-a716-446655440101"
	inboundRuleB = "550e8400-e29b-41d4-a716-446655440102"
	inboundA     = "550e8400-e29b-41d4-a716-446655440103"
)

func inboundSnapshot() c.InboundSnapshot {
	template := c.Owner{Kind: "template", ID: "sample-template"}
	router := c.Owner{Kind: "router", ID: "sample-router"}
	return c.InboundSnapshot{
		Rules: c.RouterSelectionInput{
			Template: template, Router: router,
			Shared: []c.SharedElement{{Element: c.Element{ID: inboundRuleA, Content: `{"inboundTag":["${source}"]}`, Variables: []c.VariableDefinition{{Name: "source", Type: "string"}}}, Position: "/routing/rules/0"}},
			Local:  []c.LocalContent{{ID: inboundRuleB, Content: json.RawMessage(`{"inboundTag":["lan-in"]}`), Position: "/local/rules/0"}},
		},
		Inbounds: c.RouterSelectionInput{
			Template: template, Router: router,
			Shared: []c.SharedElement{{Element: c.Element{ID: inboundA, Content: `{"tag":"lan-in"}`}, Position: "/inbounds/0"}},
		},
		TemplateOrder: []string{inboundRuleA},
		PersonalOrder: []string{inboundRuleA, inboundRuleB},
		DisabledRules: []c.ElementRef{{ID: inboundRuleA, Position: "/disabledRules/0"}},
	}
}

func TestInboundDisabledMissingValueKeepsFullIdentity(t *testing.T) {
	got, issues := c.AnalyzeInboundDependencies(inboundSnapshot())
	if got == nil || len(issues) != 0 || len(got.FullRules) != 2 || len(got.References) != 1 || got.References[0].RuleID != inboundRuleB {
		t.Fatalf("wrong active projection: result=%+v issues=%+v", got, issues)
	}
}

func TestInboundDisabledRuleDoesNotHideDuplicateIdentity(t *testing.T) {
	in := inboundSnapshot()
	in.Rules.Local[0].ID = inboundRuleA
	got, issues := c.AnalyzeInboundDependencies(in)
	if got != nil || len(issues) == 0 || issues[0].Code != "duplicate_element_id" || issues[0].OtherLocation == nil {
		t.Fatalf("duplicate identity: result=%+v issues=%+v", got, issues)
	}
}

func TestInboundDisabledStateIsValidatedBeforeContent(t *testing.T) {
	in := inboundSnapshot()
	in.DisabledInbounds = []c.ElementRef{{ID: inboundA, Position: "/disabledInbounds/0"}, {ID: inboundA, Position: "/disabledInbounds/1"}}
	got, issues := c.AnalyzeInboundDependencies(in)
	if got != nil || len(issues) == 0 || issues[0].Code != "duplicate_disabled_inbound" || issues[0].OtherLocation == nil {
		t.Fatalf("duplicate disabled inbound: result=%+v issues=%+v", got, issues)
	}
}

func TestInboundDisabledRuleStillChecksSourceAndOrder(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*c.InboundSnapshot)
		code string
	}{
		{"source", func(in *c.InboundSnapshot) {
			in.Rules.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{Source: c.ElementSource{TemplateID: in.Rules.Template.ID, ElementID: inboundA}, Mode: c.ContentModeVariables}, Position: "/overrides/0"}}
		}, "source_not_found"},
		{"order", func(in *c.InboundSnapshot) { in.PersonalOrder = []string{inboundRuleB} }, "order_element_not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := inboundSnapshot()
			tc.edit(&in)
			got, issues := c.AnalyzeInboundDependencies(in)
			if got != nil || len(issues) == 0 || issues[0].Code != tc.code {
				t.Fatalf("expected %s: result=%+v issues=%+v", tc.code, got, issues)
			}
		})
	}
}

func TestInboundActiveProjectionSelectsEffectiveContent(t *testing.T) {
	in := inboundSnapshot()
	in.DisabledRules = nil
	in.Rules.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{
		Source: c.ElementSource{TemplateID: in.Rules.Template.ID, ElementID: inboundRuleA},
		Mode:   c.ContentModeVariables,
		Values: map[string]json.RawMessage{"source": json.RawMessage(`"wan-in"`)},
	}, Position: "/overrides/0"}}
	in.Inbounds.Shared[0].Element.Content = `{"tag":"${name}"}`
	in.Inbounds.Shared[0].Element.Variables = []c.VariableDefinition{{Name: "name", Type: "string", Default: json.RawMessage(`"lan-in"`)}}
	got, issues := c.AnalyzeInboundDependencies(in)
	if got == nil || len(issues) != 0 || len(got.References) != 2 || got.References[0].RuleID != inboundRuleA || got.References[0].Status != "missing" || got.References[1].RuleID != inboundRuleB || got.References[1].Status != "resolved" {
		t.Fatalf("effective rules: result=%+v issues=%+v", got, issues)
	}
	if got.References[1].InboundID != inboundA {
		t.Fatalf("effective inbound: result=%+v issues=%+v", got, issues)
	}
}

func TestInboundActiveProjectionUsesReplacementAndExclusion(t *testing.T) {
	in := inboundSnapshot()
	in.DisabledRules = nil
	in.Rules.Shared[0].Element.Content = "{"
	in.Rules.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{
		Source:  c.ElementSource{TemplateID: in.Rules.Template.ID, ElementID: inboundRuleA},
		Mode:    c.ContentModeReplacement,
		Content: json.RawMessage(`{"inboundTag":["own-in"],"literal":"${source}"}`),
	}, Position: "/overrides/0"}}
	got, issues := c.AnalyzeInboundDependencies(in)
	if got == nil || len(issues) != 0 || len(got.References) != 2 || got.References[0].RuleID != inboundRuleA || got.References[0].Status != "missing" {
		t.Fatalf("replacement: result=%+v issues=%+v", got, issues)
	}
	in.Rules.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: in.Rules.Template.ID, ElementID: inboundRuleA}, Position: "/excluded/0"}}
	got, issues = c.AnalyzeInboundDependencies(in)
	if got == nil || len(issues) != 0 || len(got.FullRules) != 2 || len(got.References) != 1 || got.References[0].RuleID != inboundRuleB {
		t.Fatalf("excluded replacement: result=%+v issues=%+v", got, issues)
	}
}

func TestInboundReferencesUseRootArrayAndIgnoreJSONEnabled(t *testing.T) {
	in := inboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"inboundTag":["lan-in","wan-in"],"enabled":false,"extension":{"inboundTag":["fake"]},"ruleTag":"diagnostic"}`)
	got, issues := c.AnalyzeInboundDependencies(in)
	if got == nil || len(issues) != 0 || len(got.References) != 2 {
		t.Fatalf("expected two active references: result=%+v issues=%+v", got, issues)
	}
	if got.References[0].RuleID != inboundRuleB || got.References[0].Path != "/inboundTag/0" || got.References[1].Path != "/inboundTag/1" {
		t.Fatalf("wrong reference paths: %+v", got.References)
	}
}

func TestInboundReferencesSplitStringAtCommas(t *testing.T) {
	in := inboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"inboundTag":"lan-in,wan-in"}`)
	got, issues := c.AnalyzeInboundDependencies(in)
	if got == nil || len(issues) != 0 || len(got.References) != 2 || got.References[0].Path != "/inboundTag" || got.References[0].PartIndex != 0 || got.References[1].Path != "/inboundTag" || got.References[1].PartIndex != 1 {
		t.Fatalf("comma list: result=%+v issues=%+v", got, issues)
	}
}

func TestInboundReferencesIgnoreNestedKey(t *testing.T) {
	in := inboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"extension":{"inboundTag":["lan-in"]}}`)
	got, issues := c.AnalyzeInboundDependencies(in)
	if got == nil || len(issues) != 0 || len(got.References) != 0 {
		t.Fatalf("nested field became a reference: result=%+v issues=%+v", got, issues)
	}
}

func TestInboundReferenceResolutionAndMissingDraft(t *testing.T) {
	in := inboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"inboundTag":["lan-in","absent-in"]}`)
	got, issues := c.AnalyzeInboundDependencies(in)
	if got == nil || len(issues) != 0 || got.Ready || len(got.References) != 2 {
		t.Fatalf("resolution result=%+v issues=%+v", got, issues)
	}
	if got.References[0].Status != "resolved" || got.References[0].InboundID != inboundA || got.References[1].Status != "missing" || got.References[1].InboundID != "" {
		t.Fatalf("wrong reference states: %+v", got.References)
	}
}

func TestInboundCommaListDoesNotTrim(t *testing.T) {
	in := inboundSnapshot()
	wanID := "550e8400-e29b-41d4-a716-446655440104"
	in.Inbounds.Local = []c.LocalContent{{ID: wanID, Content: json.RawMessage(`{"tag":"wan-in"}`), Position: "/local/inbounds/0"}}
	in.Rules.Local[0].Content = json.RawMessage(`{"inboundTag":"lan-in, wan-in"}`)
	got, issues := c.AnalyzeInboundDependencies(in)
	if got == nil || len(issues) != 0 || got.Ready || len(got.References) != 2 || got.References[0].Status != "resolved" || got.References[1].Status != "missing" {
		t.Fatalf("string list was trimmed: result=%+v issues=%+v", got, issues)
	}
}

func TestInboundDisabledProviderLeavesMissingReference(t *testing.T) {
	in := inboundSnapshot()
	in.DisabledInbounds = []c.ElementRef{{ID: inboundA, Position: "/disabledInbounds/0"}}
	got, issues := c.AnalyzeInboundDependencies(in)
	if got == nil || len(issues) != 0 || got.Ready || len(got.References) != 1 || got.References[0].Status != "missing" {
		t.Fatalf("disabled provider: result=%+v issues=%+v", got, issues)
	}
}

func TestInboundDuplicateActiveTagIsAmbiguous(t *testing.T) {
	in := inboundSnapshot()
	in.Inbounds.Local = []c.LocalContent{{ID: "550e8400-e29b-41d4-a716-446655440104", Content: json.RawMessage(`{"tag":"lan-in"}`), Position: "/local/inbounds/0"}}
	got, issues := c.AnalyzeInboundDependencies(in)
	if got != nil || len(issues) < 2 || issues[0].Code != "ambiguous_inbound_tag" || issues[0].OtherLocation == nil || issues[0].Location.Position != "/local/inbounds/0" || issues[0].OtherLocation.Position != "/inbounds/0" || issues[1].Code != "ambiguous_inbound_reference" || issues[1].Location.Position != "/local/rules/0" {
		t.Fatalf("duplicate tag: result=%+v issues=%+v", got, issues)
	}
}

func TestInboundInvalidRecognizedFieldHasSafeLocation(t *testing.T) {
	in := inboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"inboundTag":42,"secret":"value-to-hide"}`)
	got, issues := c.AnalyzeInboundDependencies(in)
	if got != nil || len(issues) == 0 || issues[0].Code != "invalid_inbound_tag" || issues[0].Path == nil || *issues[0].Path != "/inboundTag" || issues[0].Location.Position != "/local/rules/0" || issues[0].ElementID != inboundRuleB {
		t.Fatalf("invalid field: result=%+v issues=%+v", got, issues)
	}
	if text := fmt.Sprintf("%+v", issues); strings.Contains(text, "value-to-hide") || strings.Contains(text, "secret") {
		t.Fatalf("diagnostic leaks content: %s", text)
	}
}

func TestInboundRuleMustBeObject(t *testing.T) {
	in := inboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`null`)
	got, issues := c.AnalyzeInboundDependencies(in)
	if got != nil || len(issues) == 0 || issues[0].Code != "invalid_rule_shape" || issues[0].Path == nil || *issues[0].Path != "" {
		t.Fatalf("wrong root accepted: result=%+v issues=%+v", got, issues)
	}
}

func TestUsedInboundRejectsDisableAndRemoveWithoutMutation(t *testing.T) {
	for _, action := range []c.InboundAction{c.InboundActionDisable, c.InboundActionRemove} {
		t.Run(string(action), func(t *testing.T) {
			in := inboundSnapshot()
			before := inboundSnapshot()
			candidate, issues := c.GuardInboundChange(c.InboundActionInput{Snapshot: in, Target: c.ElementRef{ID: inboundA, Position: "/action/target"}, Action: action})
			if candidate != nil || len(issues) != 1 || issues[0].Code != "inbound_in_use" || issues[0].ElementID != inboundRuleB || issues[0].Path == nil || *issues[0].Path != "/inboundTag/0" || issues[0].Location.Position != "/local/rules/0" {
				t.Fatalf("used inbound: candidate=%+v issues=%+v", candidate, issues)
			}
			if !reflect.DeepEqual(in, before) {
				t.Fatal("input snapshot changed")
			}
		})
	}
}

func TestUsedInboundReportsAllDependentRules(t *testing.T) {
	in := inboundSnapshot()
	thirdID := "550e8400-e29b-41d4-a716-446655440105"
	in.Rules.Local = append(in.Rules.Local, c.LocalContent{ID: thirdID, Content: json.RawMessage(`{"inboundTag":"lan-in"}`), Position: "/local/rules/1"})
	in.PersonalOrder = append(in.PersonalOrder, thirdID)
	candidate, issues := c.GuardInboundChange(c.InboundActionInput{Snapshot: in, Target: c.ElementRef{ID: inboundA, Position: "/action/target"}, Action: c.InboundActionRemove})
	if candidate != nil || len(issues) != 2 || issues[0].ElementID != inboundRuleB || issues[1].ElementID != thirdID {
		t.Fatalf("dependent rules: candidate=%+v issues=%+v", candidate, issues)
	}
}

func TestInboundSameCandidateRemovesLastReference(t *testing.T) {
	in := inboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"outboundTag":"direct"}`)
	in.DisabledInbounds = []c.ElementRef{{ID: inboundA, Position: "/disabledInbounds/0"}}
	candidate, issues := c.GuardInboundChange(c.InboundActionInput{Snapshot: in, Target: c.ElementRef{ID: inboundA, Position: "/action/target"}, Action: c.InboundActionDisable})
	if candidate == nil || len(issues) != 0 || candidate.TargetID != inboundA {
		t.Fatalf("same candidate should pass: candidate=%+v issues=%+v", candidate, issues)
	}
}

func TestInboundSameCandidateStillHasAnotherReference(t *testing.T) {
	in := inboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{}`)
	thirdID := "550e8400-e29b-41d4-a716-446655440105"
	in.Rules.Local = append(in.Rules.Local, c.LocalContent{ID: thirdID, Content: json.RawMessage(`{"inboundTag":"lan-in"}`), Position: "/local/rules/1"})
	in.PersonalOrder = append(in.PersonalOrder, thirdID)
	in.DisabledInbounds = []c.ElementRef{{ID: inboundA, Position: "/disabledInbounds/0"}}
	candidate, issues := c.GuardInboundChange(c.InboundActionInput{Snapshot: in, Target: c.ElementRef{ID: inboundA, Position: "/action/target"}, Action: c.InboundActionDisable})
	if candidate != nil || len(issues) != 1 || issues[0].Code != "inbound_in_use" || issues[0].ElementID != thirdID {
		t.Fatalf("remaining reference should block: candidate=%+v issues=%+v", candidate, issues)
	}
}

func TestInboundDisabledOrExcludedRuleReleasesTarget(t *testing.T) {
	for _, mode := range []string{"disabled", "excluded", "without-field"} {
		t.Run(mode, func(t *testing.T) {
			in := inboundSnapshot()
			switch mode {
			case "disabled":
				in.DisabledRules = append(in.DisabledRules, c.ElementRef{ID: inboundRuleB, Position: "/disabledRules/1"})
			case "excluded":
				in.Rules.Local = nil
				in.PersonalOrder = []string{inboundRuleA}
				in.DisabledRules = nil
				in.Rules.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: in.Rules.Template.ID, ElementID: inboundRuleA}, Position: "/excluded/0"}}
			case "without-field":
				in.Rules.Local[0].Content = json.RawMessage(`{"extension":{"inboundTag":["lan-in"]}}`)
			}
			candidate, issues := c.GuardInboundChange(c.InboundActionInput{Snapshot: in, Target: c.ElementRef{ID: inboundA, Position: "/action/target"}, Action: c.InboundActionRemove})
			if candidate == nil || len(issues) != 0 {
				t.Fatalf("unreferenced inbound blocked: candidate=%+v issues=%+v", candidate, issues)
			}
		})
	}
}

func TestInboundReenabledRuleCanBeUnreadyDraft(t *testing.T) {
	in := inboundSnapshot()
	in.DisabledRules = nil
	in.Rules.Shared[0].Element.Variables[0].Default = json.RawMessage(`"lan-in"`)
	in.Inbounds.Shared = nil
	got, issues := c.AnalyzeInboundDependencies(in)
	if got == nil || len(issues) != 0 || got.Ready || len(got.References) != 2 || got.References[0].Status != "missing" || got.References[1].Status != "missing" {
		t.Fatalf("reenabled rule should be a diagnosable draft: result=%+v issues=%+v", got, issues)
	}
}

func TestInboundAnalysisDoesNotReturnSelectedJSON(t *testing.T) {
	in := inboundSnapshot()
	in.Rules.Local[0].Content = json.RawMessage(`{"inboundTag":["lan-in"],"secret":"value-to-hide"}`)
	got, issues := c.AnalyzeInboundDependencies(in)
	if got == nil || len(issues) != 0 {
		t.Fatalf("analysis failed: result=%+v issues=%+v", got, issues)
	}
	if text := fmt.Sprintf("%+v", got); strings.Contains(text, "value-to-hide") || strings.Contains(text, `"inboundTag"`) {
		t.Fatalf("analysis exposes selected JSON: %s", text)
	}
}

func TestInboundAnalysisResultAndInputsAreIndependent(t *testing.T) {
	in := inboundSnapshot()
	before := inboundSnapshot()
	first, issues := c.AnalyzeInboundDependencies(in)
	if first == nil || len(issues) != 0 || len(first.References) != 1 || len(first.FullRules) != 2 {
		t.Fatalf("analysis failed: result=%+v issues=%+v", first, issues)
	}
	first.FullRules[0].Source.TemplateID = "changed"
	first.References[0].Status = "changed"
	second, issues := c.AnalyzeInboundDependencies(in)
	if second == nil || len(issues) != 0 || second.FullRules[0].Source.TemplateID != in.Rules.Template.ID || second.References[0].Status != "resolved" || !reflect.DeepEqual(in, before) {
		t.Fatalf("analysis aliases input or previous result: result=%+v issues=%+v", second, issues)
	}
}
