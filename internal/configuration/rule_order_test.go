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
	orderA = "550e8400-e29b-41d4-a716-446655440001"
	orderB = "550e8400-e29b-41d4-a716-446655440002"
	orderC = "550e8400-e29b-41d4-a716-446655440003"
	orderL = "550e8400-e29b-41d4-a716-446655440004"
	orderX = "550e8400-e29b-41d4-a716-446655440005"
	orderY = "550e8400-e29b-41d4-a716-446655440006"
)

func orderInput(shared, local, template, personal []string) c.RuleOrderInput {
	in := c.RuleOrderInput{
		Template:      c.Owner{Kind: "template", ID: "template-a"},
		Router:        c.Owner{Kind: "router", ID: "router-a"},
		TemplateOrder: template,
		PersonalOrder: personal,
	}
	for i, id := range shared {
		in.Shared = append(in.Shared, c.ElementRef{ID: id, Position: "/routing/rules/" + string(rune('0'+i))})
	}
	for i, id := range local {
		in.Local = append(in.Local, c.ElementRef{ID: id, Position: "/local/" + string(rune('0'+i))})
	}
	return in
}

func orderIDs(entries []c.RuleOrderEntry) []string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	return ids
}

func TestRuleOrderInitialAndPersonal(t *testing.T) {
	in := orderInput([]string{orderA, orderB, orderC}, nil, []string{orderA, orderB, orderC}, nil)
	got, issues := c.AssembleRuleOrder(in)
	if got == nil || len(issues) != 0 || !reflect.DeepEqual(orderIDs(got.Full), []string{orderA, orderB, orderC}) || !reflect.DeepEqual(orderIDs(got.Visible), []string{orderA, orderB, orderC}) {
		t.Fatalf("default order: %+v %+v", got, issues)
	}
	in.TemplateOrder = []string{orderB, orderA, orderC}
	got, issues = c.AssembleRuleOrder(in)
	if got == nil || len(issues) != 0 || !reflect.DeepEqual(orderIDs(got.Full), in.TemplateOrder) {
		t.Fatalf("default follows moved template: %+v %+v", got, issues)
	}
	in.Local = []c.ElementRef{{ID: orderL, Position: "/local/0"}}
	in.PersonalOrder = []string{orderA, orderL, orderB, orderC}
	got, issues = c.AssembleRuleOrder(in)
	if got == nil || len(issues) != 0 || !reflect.DeepEqual(orderIDs(got.Full), in.PersonalOrder) || got.Full[1].Origin != "local" || got.Full[2].Origin != "shared" || got.Full[2].Source == nil {
		t.Fatalf("personal order: %+v %+v", got, issues)
	}
}

func TestRuleOrderEmptyAndPresentEmpty(t *testing.T) {
	in := orderInput(nil, nil, nil, nil)
	got, issues := c.AssembleRuleOrder(in)
	if got == nil || len(issues) != 0 || got.Full == nil || got.Visible == nil || len(got.Full) != 0 {
		t.Fatalf("empty inherited order: %+v %+v", got, issues)
	}
	in.PersonalOrder = []string{}
	got, issues = c.AssembleRuleOrder(in)
	if got == nil || len(issues) != 0 || got.Full == nil || got.Visible == nil || len(got.Full) != 0 {
		t.Fatalf("empty saved order: %+v %+v", got, issues)
	}
}

func orderFailure(t *testing.T, in c.RuleOrderInput, code string) c.SelectionIssue {
	t.Helper()
	got, issues := c.AssembleRuleOrder(in)
	if got != nil || len(issues) == 0 || issues[0].Code != code {
		t.Fatalf("want %s, got %+v %+v", code, got, issues)
	}
	return issues[0]
}

func TestRuleOrderExclusionAndRestore(t *testing.T) {
	in := orderInput([]string{orderA, orderB}, []string{orderL}, []string{orderA, orderB}, []string{orderA, orderB, orderL})
	in.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: in.Template.ID, ElementID: orderB}, Position: "/excluded/0"}}
	got, issues := c.AssembleRuleOrder(in)
	if got == nil || len(issues) != 0 || !reflect.DeepEqual(orderIDs(got.Full), in.PersonalOrder) || !reflect.DeepEqual(orderIDs(got.Visible), []string{orderA, orderL}) {
		t.Fatalf("excluded: %+v %+v", got, issues)
	}
	in.Exclusions = nil
	got, issues = c.AssembleRuleOrder(in)
	if got == nil || len(issues) != 0 || !reflect.DeepEqual(orderIDs(got.Visible), in.PersonalOrder) {
		t.Fatalf("restored: %+v %+v", got, issues)
	}
}

func TestRuleOrderDuplicateAndMissing(t *testing.T) {
	in := orderInput([]string{orderA, orderB}, []string{orderL}, []string{orderA, orderB}, []string{orderA, orderB, orderB, orderL})
	in.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: in.Template.ID, ElementID: orderB}, Position: "/excluded/0"}}
	orderFailure(t, in, "duplicate_order_element")
	in.PersonalOrder = []string{orderA, orderB}
	orderFailure(t, in, "order_element_not_found")
	in.PersonalOrder = []string{orderA, orderB, orderX}
	orderFailure(t, in, "order_element_not_found")
	in.PersonalOrder = nil
	orderFailure(t, in, "order_element_not_found")
}

func TestRuleOrderInvalidMetadataAndSafeErrors(t *testing.T) {
	in := orderInput([]string{orderA}, nil, []string{orderA}, nil)
	in.Template.Kind = "router"
	orderFailure(t, in, "invalid_element_record")
	in = orderInput([]string{orderA}, nil, []string{orderA}, nil)
	in.Router.ID = string([]byte{0xff})
	orderFailure(t, in, "invalid_element_record")
	in = orderInput([]string{"bad-uuid"}, nil, []string{orderA}, nil)
	issue := orderFailure(t, in, "invalid_element_id")
	if issue.ElementID != "" {
		t.Fatalf("raw UUID diagnostic: %+v", issue)
	}

	in = orderInput([]string{orderA}, nil, []string{orderA}, []string{"bad-uuid"})
	issue = orderFailure(t, in, "invalid_element_id")
	if issue.ElementID != "" || issue.Location.Owner != in.Router || issue.Location.Position != "/order/0" {
		t.Fatalf("unsafe UUID diagnostic: %+v", issue)
	}
	if strings.Contains(fmt.Sprintf("%+v", issue), "bad-uuid") {
		t.Fatalf("raw value leaked: %+v", issue)
	}
	in.PersonalOrder = []string{orderA, orderA}
	issue = orderFailure(t, in, "duplicate_order_element")
	if issue.ElementID != orderA || issue.Location.Position != "/order/1" || issue.OtherLocation == nil || issue.OtherLocation.Position != "/order/0" {
		t.Fatalf("duplicate diagnostic: %+v", issue)
	}
}

func TestInsertLocalRuleAtEveryIndex(t *testing.T) {
	for _, tc := range []struct {
		index int
		want  []string
	}{
		{0, []string{orderL, orderA, orderB}},
		{1, []string{orderA, orderL, orderB}},
		{2, []string{orderA, orderB, orderL}},
	} {
		in := orderInput([]string{orderA, orderB}, nil, []string{orderA, orderB}, nil)
		got, issues := c.InsertLocalRule(in, c.ElementRef{ID: orderL, Position: "/local/0"}, tc.index)
		if got == nil || len(issues) != 0 || !reflect.DeepEqual(got.Full, tc.want) || !reflect.DeepEqual(got.PersonalOrder, tc.want) || in.PersonalOrder != nil {
			t.Fatalf("index %d: %+v %+v", tc.index, got, issues)
		}
	}
}

func TestInsertLocalRuleRejectsInvalidIndexAndDuplicate(t *testing.T) {
	in := orderInput([]string{orderA, orderB}, nil, []string{orderA, orderB}, nil)
	for _, index := range []int{-1, 3} {
		got, issues := c.InsertLocalRule(in, c.ElementRef{ID: orderL, Position: "/local/0"}, index)
		if got != nil || len(issues) == 0 || issues[0].Code != "invalid_order_index" {
			t.Fatalf("index %d: %+v %+v", index, got, issues)
		}
	}
	got, issues := c.InsertLocalRule(in, c.ElementRef{ID: orderA, Position: "/local/0"}, 1)
	if got != nil || len(issues) == 0 || issues[0].Code != "duplicate_order_element" {
		t.Fatalf("duplicate: %+v %+v", got, issues)
	}
}

func TestMoveRuleUsesRemainingListIndex(t *testing.T) {
	in := orderInput([]string{orderA, orderB}, []string{orderL}, []string{orderA, orderB}, []string{orderA, orderL, orderB})
	got, issues := c.MoveRule(in, orderB, 0)
	if got == nil || len(issues) != 0 || !reflect.DeepEqual(got.Full, []string{orderB, orderA, orderL}) || !reflect.DeepEqual(in.PersonalOrder, []string{orderA, orderL, orderB}) {
		t.Fatalf("shared across local: %+v %+v", got, issues)
	}
	in = orderInput([]string{orderA, orderB, orderC}, nil, []string{orderA, orderB, orderC}, nil)
	got, issues = c.MoveRule(in, orderA, 2)
	if got == nil || len(issues) != 0 || !reflect.DeepEqual(got.Full, []string{orderB, orderC, orderA}) {
		t.Fatalf("remaining index: %+v %+v", got, issues)
	}
}

func TestMoveRuleRejectsBadIndexAndUnknownID(t *testing.T) {
	in := orderInput([]string{orderA, orderB, orderC}, nil, []string{orderA, orderB, orderC}, nil)
	for _, index := range []int{-1, 3} {
		got, issues := c.MoveRule(in, orderA, index)
		if got != nil || len(issues) == 0 || issues[0].Code != "invalid_order_index" {
			t.Fatalf("index %d: %+v %+v", index, got, issues)
		}
	}
	got, issues := c.MoveRule(in, orderL, 1)
	if got != nil || len(issues) == 0 || issues[0].Code != "order_element_not_found" {
		t.Fatalf("unknown: %+v %+v", got, issues)
	}
}

func TestReconcileRuleOrderTemplateChanges(t *testing.T) {
	cases := []struct {
		name     string
		old      []string
		local    []string
		personal []string
		next     []string
		want     []string
	}{
		{"new shared numeric index", []string{orderA, orderB, orderC}, nil, []string{orderC, orderA, orderB}, []string{orderA, orderX, orderB, orderC}, []string{orderC, orderX, orderA, orderB}},
		{"new shared shifts local", []string{orderA, orderB}, []string{orderL}, []string{orderA, orderL, orderB}, []string{orderA, orderX, orderB}, []string{orderA, orderX, orderL, orderB}},
		{"deleted shared", []string{orderA, orderB, orderC}, []string{orderL}, []string{orderC, orderL, orderB, orderA}, []string{orderA, orderC}, []string{orderC, orderL, orderA}},
		{"batch removal and additions", []string{orderA, orderB, orderC}, []string{orderL}, []string{orderC, orderL, orderB, orderA}, []string{orderX, orderA, orderY, orderC}, []string{orderX, orderC, orderY, orderL, orderA}},
		{"personal survives template move", []string{orderA, orderB}, []string{orderL}, []string{orderA, orderL, orderB}, []string{orderB, orderA}, []string{orderA, orderL, orderB}},
		{"default follows template move", []string{orderA, orderB}, nil, nil, []string{orderB, orderA}, []string{orderB, orderA}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := orderInput(tc.old, tc.local, tc.old, tc.personal)
			next := orderInput(tc.next, nil, tc.next, nil)
			got, issues := c.ReconcileRuleOrder(in, next.Shared, tc.next)
			if got == nil || len(issues) != 0 || !reflect.DeepEqual(got.Full, tc.want) {
				t.Fatalf("got %+v %+v; want %v", got, issues, tc.want)
			}
			if (got.PersonalOrder == nil) != (tc.personal == nil) {
				t.Fatalf("lost absence: %+v", got)
			}
		})
	}
}

func TestReconcileRuleOrderRejectsDamagedPreviousList(t *testing.T) {
	in := orderInput([]string{orderA, orderB}, nil, []string{orderA, orderB}, []string{orderA})
	next := orderInput([]string{orderA, orderB, orderX}, nil, []string{orderA, orderB, orderX}, nil)
	got, issues := c.ReconcileRuleOrder(in, next.Shared, next.TemplateOrder)
	if got != nil || len(issues) == 0 || issues[0].Code != "order_element_not_found" {
		t.Fatalf("damaged old order accepted: %+v %+v", got, issues)
	}
}

func TestReconcileDoesNotCleanDeletedSourceBindings(t *testing.T) {
	in := orderInput([]string{orderA, orderB}, nil, []string{orderA, orderB}, []string{orderB, orderA})
	in.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: in.Template.ID, ElementID: orderB}, Position: "/excluded/0"}}
	next := orderInput([]string{orderA}, nil, []string{orderA}, nil)
	got, issues := c.ReconcileRuleOrder(in, next.Shared, next.TemplateOrder)
	if got == nil || len(issues) != 0 || !reflect.DeepEqual(got.Full, []string{orderA}) || len(in.Exclusions) != 1 {
		t.Fatalf("candidate: %+v %+v", got, issues)
	}
	next.Exclusions = in.Exclusions
	orderFailure(t, next, "source_not_found")
}

func TestReconcileEmptyDefaultReturnsEmptyCandidate(t *testing.T) {
	in := orderInput(nil, nil, nil, nil)
	got, issues := c.ReconcileRuleOrder(in, nil, nil)
	if got == nil || len(issues) != 0 || got.Full == nil || len(got.Full) != 0 || got.PersonalOrder != nil {
		t.Fatalf("empty default: %+v %+v", got, issues)
	}
}

func TestResetRuleOrderKeepsLocalSlotsAndExclusion(t *testing.T) {
	in := orderInput([]string{orderA, orderB, orderC}, []string{orderL}, []string{orderA, orderB, orderC}, []string{orderB, orderL, orderA, orderC})
	in.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: in.Template.ID, ElementID: orderB}, Position: "/excluded/0"}}
	got, issues := c.ResetRuleOrder(in)
	want := []string{orderA, orderL, orderB, orderC}
	if got == nil || len(issues) != 0 || !reflect.DeepEqual(got.Full, want) || !reflect.DeepEqual(got.PersonalOrder, want) || !reflect.DeepEqual(in.PersonalOrder, []string{orderB, orderL, orderA, orderC}) {
		t.Fatalf("reset: %+v %+v", got, issues)
	}
	in.PersonalOrder = got.PersonalOrder
	assembled, issues := c.AssembleRuleOrder(in)
	if assembled == nil || len(issues) != 0 || !reflect.DeepEqual(orderIDs(assembled.Visible), []string{orderA, orderL, orderC}) {
		t.Fatalf("excluded after reset: %+v %+v", assembled, issues)
	}
}

func TestResetRuleOrderWithoutLocalReturnsInheritance(t *testing.T) {
	in := orderInput([]string{orderA, orderB}, nil, []string{orderA, orderB}, []string{orderB, orderA})
	got, issues := c.ResetRuleOrder(in)
	if got == nil || len(issues) != 0 || !reflect.DeepEqual(got.Full, []string{orderA, orderB}) || got.PersonalOrder != nil {
		t.Fatalf("reset default: %+v %+v", got, issues)
	}
}

func TestResetRuleOrderKeepsReplacementData(t *testing.T) {
	in := orderedSelectionInput()
	in.Selection.Shared = append(in.Selection.Shared, c.SharedElement{Element: c.Element{ID: orderB, Content: "{"}, Position: "/routing/rules/1"})
	in.Selection.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{Source: c.ElementSource{TemplateID: in.Selection.Template.ID, ElementID: orderB}, Mode: c.ContentModeReplacement, Content: json.RawMessage(`{"own":true}`)}, Position: "/overrides/0"}}
	in.Selection.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: in.Selection.Template.ID, ElementID: orderB}, Position: "/excluded/0"}}
	in.TemplateOrder = []string{orderA, orderB}
	in.PersonalOrder = []string{orderB, orderL, orderA}
	pure := orderInputFromSelectionForTest(in)
	candidate, issues := c.ResetRuleOrder(pure)
	if candidate == nil || len(issues) != 0 || !reflect.DeepEqual(candidate.Full, []string{orderA, orderL, orderB}) {
		t.Fatalf("reset candidate: %+v %+v", candidate, issues)
	}
	in.PersonalOrder = candidate.PersonalOrder
	hidden, issues := c.SelectOrderedRouterElements(in)
	if hidden == nil || len(issues) != 0 || len(hidden.Visible) != 2 {
		t.Fatalf("hidden replacement: %+v %+v", hidden, issues)
	}
	in.Selection.Exclusions = nil
	visible, issues := c.SelectOrderedRouterElements(in)
	if visible == nil || len(issues) != 0 || len(visible.Visible) != 3 || visible.Visible[2].Effective.Mode != c.ContentModeReplacement || string(visible.Visible[2].Effective.Content) != `{"own":true}` {
		t.Fatalf("restored replacement: %+v %+v", visible, issues)
	}
}

func orderInputFromSelectionForTest(in c.OrderedRouterInput) c.RuleOrderInput {
	pure := c.RuleOrderInput{Template: in.Selection.Template, Router: in.Selection.Router, TemplateOrder: in.TemplateOrder, PersonalOrder: in.PersonalOrder, Exclusions: in.Selection.Exclusions}
	for _, shared := range in.Selection.Shared {
		pure.Shared = append(pure.Shared, c.ElementRef{ID: shared.Element.ID, Position: shared.Position})
	}
	for _, local := range in.Selection.Local {
		pure.Local = append(pure.Local, c.ElementRef{ID: local.ID, Position: local.Position})
	}
	return pure
}

func orderedSelectionInput() c.OrderedRouterInput {
	selection := selectionInput()
	selection.Shared = []c.SharedElement{{Element: c.Element{ID: orderA, Content: `{"port":"${port}"}`, Variables: []c.VariableDefinition{{Name: "port", Type: "number", Default: json.RawMessage(`443`)}}}, Position: "/routing/rules/0"}}
	selection.Local = []c.LocalContent{{ID: orderL, Content: json.RawMessage(` {"literal":"${port}","x":null} `), Position: "/local/0"}}
	return c.OrderedRouterInput{Selection: selection, TemplateOrder: []string{orderA}, PersonalOrder: []string{orderL, orderA}}
}

func TestOrderedSelectionKeepsChosenContent(t *testing.T) {
	in := orderedSelectionInput()
	got, issues := c.SelectOrderedRouterElements(in)
	if got == nil || len(issues) != 0 || !reflect.DeepEqual(orderIDs(got.Full), []string{orderL, orderA}) || len(got.Visible) != 2 {
		t.Fatalf("ordered selection: %+v %+v", got, issues)
	}
	if got.Visible[0].ID != orderL || string(got.Visible[0].Effective.Content) != ` {"literal":"${port}","x":null} ` || got.Visible[0].Effective.Origin != "local" {
		t.Fatalf("local content: %+v", got.Visible[0])
	}
	if got.Visible[1].ID != orderA || string(got.Visible[1].Effective.Content) != `{"port":443}` || got.Visible[1].Effective.Origin != "shared" {
		t.Fatalf("shared content: %+v", got.Visible[1])
	}
}

func TestOrderedSelectionFullReplacementIsLiteral(t *testing.T) {
	in := orderedSelectionInput()
	in.Selection.Shared[0].Element.Content = "{"
	in.Selection.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{Source: c.ElementSource{TemplateID: in.Selection.Template.ID, ElementID: orderA}, Mode: c.ContentModeReplacement, Content: json.RawMessage(` {"literal":"${port}","x":null} `)}, Position: "/overrides/0"}}
	got, issues := c.SelectOrderedRouterElements(in)
	if got == nil || len(issues) != 0 || len(got.Visible) != 2 || got.Visible[1].Effective.Mode != c.ContentModeReplacement || string(got.Visible[1].Effective.Content) != ` {"literal":"${port}","x":null} ` {
		t.Fatalf("replacement: %+v %+v", got, issues)
	}
}

func TestOrderedSelectionExcludedAndRestored(t *testing.T) {
	in := orderedSelectionInput()
	in.Selection.Shared = append(in.Selection.Shared, c.SharedElement{Element: c.Element{ID: orderB, Content: "{"}, Position: "/routing/rules/1"})
	in.TemplateOrder = []string{orderA, orderB}
	in.PersonalOrder = []string{orderA, orderB, orderL}
	in.Selection.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: in.Selection.Template.ID, ElementID: orderB}, Position: "/excluded/0"}}
	got, issues := c.SelectOrderedRouterElements(in)
	if got == nil || len(issues) != 0 || !reflect.DeepEqual(orderIDs(got.Full), in.PersonalOrder) || len(got.Visible) != 2 || got.Visible[0].ID != orderA || got.Visible[1].ID != orderL {
		t.Fatalf("excluded: %+v %+v", got, issues)
	}
	in.Selection.Exclusions = nil
	got, issues = c.SelectOrderedRouterElements(in)
	if got != nil || len(issues) == 0 || issues[0].Code != "invalid_json" {
		t.Fatalf("restored invalid content: %+v %+v", got, issues)
	}
}

func TestOrderedSelectionAtomicContentAndOrderFailures(t *testing.T) {
	in := orderedSelectionInput()
	in.Selection.Shared[0].Element.Content = `{"bad":null}`
	got, issues := c.SelectOrderedRouterElements(in)
	if got != nil || len(issues) == 0 || issues[0].Code != "null_not_allowed" {
		t.Fatalf("content failure: %+v %+v", got, issues)
	}
	in = orderedSelectionInput()
	in.PersonalOrder = []string{orderA}
	got, issues = c.SelectOrderedRouterElements(in)
	if got != nil || len(issues) == 0 || issues[0].Code != "order_element_not_found" {
		t.Fatalf("omitted local: %+v %+v", got, issues)
	}
	in.PersonalOrder = []string{orderA, orderA, orderL}
	got, issues = c.SelectOrderedRouterElements(in)
	if got != nil || len(issues) == 0 || issues[0].Code != "duplicate_order_element" {
		t.Fatalf("duplicate order: %+v %+v", got, issues)
	}
	in = orderedSelectionInput()
	in.Selection.Shared = append(in.Selection.Shared, c.SharedElement{Element: c.Element{ID: orderB, Content: "{"}, Position: "/routing/rules/1"})
	in.TemplateOrder = []string{orderA, orderB}
	in.PersonalOrder = []string{orderA, orderB, orderB, orderL}
	in.Selection.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: in.Selection.Template.ID, ElementID: orderB}, Position: "/excluded/0"}}
	got, issues = c.SelectOrderedRouterElements(in)
	if got != nil || len(issues) == 0 || issues[0].Code != "duplicate_order_element" {
		t.Fatalf("duplicate excluded order: %+v %+v", got, issues)
	}
}

func TestRuleOrderResultsDoNotAliasInputsOrEachOther(t *testing.T) {
	in := orderInput([]string{orderA}, []string{orderL}, []string{orderA}, []string{orderA, orderL})
	got, issues := c.AssembleRuleOrder(in)
	if got == nil || len(issues) != 0 {
		t.Fatalf("order: %+v %+v", got, issues)
	}
	got.Full[0].Source.TemplateID = "changed"
	got.Full[0].ID = orderB
	if got.Visible[0].Source.TemplateID != "template-a" || got.Visible[0].ID != orderA || in.PersonalOrder[0] != orderA {
		t.Fatalf("alias between result lists or input: %+v", got)
	}
	again, issues := c.AssembleRuleOrder(in)
	if again == nil || len(issues) != 0 || again.Full[0].ID != orderA || again.Full[0].Source.TemplateID != "template-a" {
		t.Fatalf("later call changed: %+v %+v", again, issues)
	}
}

func TestOrderedSelectionBuffersAreIndependent(t *testing.T) {
	in := orderedSelectionInput()
	original := string(in.Selection.Local[0].Content)
	got, issues := c.SelectOrderedRouterElements(in)
	if got == nil || len(issues) != 0 {
		t.Fatalf("selection: %+v %+v", got, issues)
	}
	got.Visible[0].Effective.Content[0] = 'x'
	got.Visible[1].Effective.Source.TemplateID = "changed"
	got.Full[1].Source.TemplateID = "changed"
	got.Full[0].ID = orderX
	if string(in.Selection.Local[0].Content) != original || in.PersonalOrder[0] != orderL {
		t.Fatal("input changed")
	}
	again, issues := c.SelectOrderedRouterElements(in)
	if again == nil || len(issues) != 0 || string(again.Visible[0].Effective.Content) != original || again.Visible[1].Effective.Source.TemplateID != "template-a" || again.Full[0].ID != orderL {
		t.Fatalf("later result changed: %+v %+v", again, issues)
	}
}
