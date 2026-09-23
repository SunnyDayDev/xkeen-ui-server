package configuration_test

import (
	"encoding/json"
	"fmt"
	c "github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"reflect"
	"strings"
	"testing"
)

const selectionID = "550e8400-e29b-41d4-a716-446655440010"
const selectionLocalID = "550e8400-e29b-41d4-a716-446655440011"

func sharedSelection() c.RouterSelectionInput {
	in := selectionInput()
	in.Shared = []c.SharedElement{{Element: c.Element{ID: selectionID, Content: `{"port":"${port}"}`, Variables: []c.VariableDefinition{{Name: "port", Type: "number", Default: json.RawMessage(`443`)}}}, Position: "/outbounds/0"}}
	return in
}

func selectionSource(in c.RouterSelectionInput) c.ElementSource {
	return c.ElementSource{TemplateID: in.Template.ID, ElementID: selectionID}
}

func selected(t *testing.T, in c.RouterSelectionInput) *c.RouterSelection {
	t.Helper()
	got, issues := c.SelectRouterElements(in)
	if got == nil || len(issues) != 0 {
		t.Fatalf("selection failed: %+v", issues)
	}
	return got
}

func TestSelectionSharedDefaults(t *testing.T) {
	in := sharedSelection()
	want := c.EffectiveElement{Content: json.RawMessage(`{"port":443}`), Origin: "shared", Mode: c.ContentModeVariables, Location: c.ElementLocation{Owner: in.Template, Position: "/outbounds/0"}}
	source := selectionSource(in)
	want.Source = &source
	got := selected(t, in)
	if len(got.Elements) != 1 || !reflect.DeepEqual(got.Elements[selectionID], want) {
		t.Fatalf("got %+v", got.Elements)
	}
}

func selectionInput() c.RouterSelectionInput {
	return c.RouterSelectionInput{Template: c.Owner{Kind: "template", ID: "template-a"}, Router: c.Owner{Kind: "router", ID: "router-a"}}
}

func TestSelectionEmpty(t *testing.T) {
	got, issues := c.SelectRouterElements(selectionInput())
	if got == nil || got.Elements == nil || len(got.Elements) != 0 || len(issues) != 0 {
		t.Fatalf("want successful empty set, got %+v, %+v", got, issues)
	}
}

func TestSelectionPersonalValuesAndTemplateEdits(t *testing.T) {
	in := sharedSelection()
	in.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{Source: selectionSource(in), Mode: c.ContentModeVariables, Values: map[string]json.RawMessage{"port": json.RawMessage(`8443`)}}, Position: "/overrides/0"}}
	for _, tc := range []struct{ raw, want string }{{`{"port":"${port}"}`, `{"port":8443}`}, {`{"port":"${port}","extension":true}`, `{"extension":true,"port":8443}`}} {
		in.Shared[0].Element.Content = tc.raw
		if got := selected(t, in).Elements[selectionID]; string(got.Content) != tc.want {
			t.Fatalf("got %s; want %s", got.Content, tc.want)
		}
	}
	if string(in.Overrides[0].Override.Values["port"]) != `8443` {
		t.Fatal("changed personal value")
	}
}

func TestSelectionSharedReplacement(t *testing.T) {
	in := sharedSelection()
	in.Shared[0].Element.Content = `{`
	in.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{Source: selectionSource(in), Mode: c.ContentModeReplacement, Content: json.RawMessage(` {"literal":"${port}","x":null} `), Values: map[string]json.RawMessage{"port": nil}}, Position: "/overrides/0"}}
	got := selected(t, in).Elements[selectionID]
	if string(got.Content) != ` {"literal":"${port}","x":null} ` || got.Mode != c.ContentModeReplacement || got.Source == nil || *got.Source != selectionSource(in) {
		t.Fatalf("wrong replacement: %+v", got)
	}
}

func TestSelectionLiteralLocal(t *testing.T) {
	in := sharedSelection()
	in.Shared[0].Element.Content = `{"tag":"demo"}`
	raw := json.RawMessage("\n {\"tag\":\"demo\",\"extension\":[\"${host}\",\"$$\",\"https://${host}\",9007199254740993,1e+03]} \n")
	in.Local = []c.LocalContent{{ID: selectionLocalID, Content: raw, Position: "/local/0"}}
	got := selected(t, in)
	local := got.Elements[selectionLocalID]
	if len(got.Elements) != 2 || string(local.Content) != string(raw) || local.Origin != "local" || local.Source != nil || local.Mode != "" || local.Location != (c.ElementLocation{Owner: in.Router, Position: "/local/0"}) {
		t.Fatalf("bad local result: %+v", local)
	}
}

func TestSelectionLocalRoots(t *testing.T) {
	for _, raw := range []string{`null`, `false`, `443`, `"${host}"`, `[]`, `{"nested":[null]}`} {
		t.Run(raw, func(t *testing.T) {
			in := selectionInput()
			in.Local = []c.LocalContent{{ID: selectionLocalID, Content: json.RawMessage(raw), Position: "/local/0"}}
			if got := selected(t, in).Elements[selectionLocalID]; string(got.Content) != raw {
				t.Fatalf("got %s", got.Content)
			}
		})
	}
}

func selectionFailure(t *testing.T, in c.RouterSelectionInput, code string) c.SelectionIssue {
	t.Helper()
	got, issues := c.SelectRouterElements(in)
	if got != nil || len(issues) == 0 || issues[0].Code != code {
		t.Fatalf("want %s, got %+v, %+v", code, got, issues)
	}
	return issues[0]
}

func TestSelectionActiveNull(t *testing.T) {
	for _, where := range []string{"template", "default", "value"} {
		t.Run(where, func(t *testing.T) {
			in := sharedSelection()
			switch where {
			case "template":
				in.Shared[0].Element.Content = `{"x":null}`
			case "default":
				in.Shared[0].Element.Variables[0].Default = json.RawMessage(`null`)
			case "value":
				in.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{Source: selectionSource(in), Mode: c.ContentModeVariables, Values: map[string]json.RawMessage{"port": json.RawMessage(`null`)}}, Position: "/overrides/0"}}
			}
			issue := selectionFailure(t, in, "null_not_allowed")
			if issue.Source != where {
				t.Fatalf("source: %+v", issue)
			}
		})
	}
}

func TestSelectionAbsentLocalJSON(t *testing.T) {
	in := selectionInput()
	in.Local = []c.LocalContent{{ID: selectionLocalID, Position: "/local/0"}}
	issue := selectionFailure(t, in, "invalid_json")
	if issue.Location != (c.ElementLocation{Owner: in.Router, Position: "/local/0"}) || issue.Source != "content" {
		t.Fatalf("location: %+v", issue)
	}
}

func TestSelectionInvalidLocalJSON(t *testing.T) {
	for _, tc := range []struct{ name, raw, code string }{
		{"empty", "", "invalid_json"}, {"spaces", " \n\t", "invalid_json"}, {"syntax", `{"x":`, "invalid_json"}, {"roots", `{} []`, "invalid_json"}, {"utf8", string([]byte{34, 255, 34}), "invalid_json"},
		{"nested_duplicate", `{"a":{"x":1,"x":2}}`, "duplicate_json_key"}, {"escaped_duplicate", `{"x":1,"\u0078":2}`, "duplicate_json_key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := sharedSelection()
			in.Local = []c.LocalContent{{ID: selectionLocalID, Content: json.RawMessage(tc.raw), Position: "/local/0"}}
			issue := selectionFailure(t, in, tc.code)
			if issue.ElementID != selectionLocalID || issue.Location.Owner != in.Router || issue.Source != "content" {
				t.Fatalf("diagnostic: %+v", issue)
			}
		})
	}
}

func TestSelectionIdentityCollisions(t *testing.T) {
	for _, kind := range []string{"shared", "local", "shared_local", "excluded_shared"} {
		t.Run(kind, func(t *testing.T) {
			in := sharedSelection()
			var first, second c.ElementLocation
			switch kind {
			case "shared":
				copy := in.Shared[0]
				copy.Position = "/routing/rules/0"
				in.Shared = append(in.Shared, copy)
				first = c.ElementLocation{Owner: in.Template, Position: "/outbounds/0"}
				second = c.ElementLocation{Owner: in.Template, Position: copy.Position}
			case "local":
				in.Local = []c.LocalContent{{ID: selectionLocalID, Content: json.RawMessage(`{"enabled":false}`), Position: "/local/0"}, {ID: selectionLocalID, Content: json.RawMessage(`{}`), Position: "/local/1"}}
				first = c.ElementLocation{Owner: in.Router, Position: "/local/0"}
				second = c.ElementLocation{Owner: in.Router, Position: "/local/1"}
			default:
				in.Local = []c.LocalContent{{ID: selectionID, Content: json.RawMessage(`{}`), Position: "/local/0"}}
				first = c.ElementLocation{Owner: in.Template, Position: "/outbounds/0"}
				second = c.ElementLocation{Owner: in.Router, Position: "/local/0"}
			}
			if kind == "excluded_shared" {
				in.Exclusions = []c.SharedExclusion{{Source: selectionSource(in), Position: "/exclusions/0"}}
				in.Shared[0].Element.Content = `{`
			}
			issue := selectionFailure(t, in, "duplicate_element_id")
			if issue.Location != second || issue.OtherLocation == nil || *issue.OtherLocation != first {
				t.Fatalf("conflict positions: %+v", issue)
			}
		})
	}
}

func TestSelectionNewSharedCollision(t *testing.T) {
	in := selectionInput()
	in.Local = []c.LocalContent{{ID: selectionLocalID, Content: json.RawMessage(`{"x":1}`), Position: "/local/0"}}
	selected(t, in)
	in.Shared = []c.SharedElement{{Element: c.Element{ID: selectionLocalID, Content: `{}`}, Position: "/routing/rules/0"}}
	before, _ := json.Marshal(in)
	selectionFailure(t, in, "duplicate_element_id")
	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Fatal("changed colliding records")
	}
}

func TestSelectionMetadata(t *testing.T) {
	badUTF8 := string([]byte{255})
	cases := []struct {
		name, code string
		edit       func(*c.RouterSelectionInput)
	}{
		{"template_empty", "invalid_element_record", func(in *c.RouterSelectionInput) { in.Template.ID = "" }},
		{"router_empty", "invalid_element_record", func(in *c.RouterSelectionInput) { in.Router.ID = "" }},
		{"template_utf8", "invalid_element_record", func(in *c.RouterSelectionInput) { in.Template.ID = badUTF8 }},
		{"router_utf8", "invalid_element_record", func(in *c.RouterSelectionInput) { in.Router.ID = badUTF8 }},
		{"template_kind", "invalid_element_record", func(in *c.RouterSelectionInput) { in.Template.Kind = "router" }},
		{"router_kind", "invalid_element_record", func(in *c.RouterSelectionInput) { in.Router.Kind = "template" }},
		{"shared_position", "invalid_element_record", func(in *c.RouterSelectionInput) { in.Shared[0].Position = "/bad~" }},
		{"shared_uuid", "invalid_element_id", func(in *c.RouterSelectionInput) { in.Shared[0].Element.ID = "invalid-id" }},
		{"local_position", "invalid_element_record", func(in *c.RouterSelectionInput) { in.Local[0].Position = "not-pointer" }},
		{"local_utf8_position", "invalid_element_record", func(in *c.RouterSelectionInput) { in.Local[0].Position = "/" + badUTF8 }},
		{"local_uuid", "invalid_element_id", func(in *c.RouterSelectionInput) { in.Local[0].ID = "invalid-id" }},
		{"override_position", "invalid_element_record", func(in *c.RouterSelectionInput) { in.Overrides[0].Position = "/bad~2" }},
		{"override_template_empty", "invalid_element_record", func(in *c.RouterSelectionInput) { in.Overrides[0].Override.Source.TemplateID = "" }},
		{"override_template_utf8", "invalid_element_record", func(in *c.RouterSelectionInput) { in.Overrides[0].Override.Source.TemplateID = badUTF8 }},
		{"override_uuid", "invalid_element_id", func(in *c.RouterSelectionInput) { in.Overrides[0].Override.Source.ElementID = "invalid-id" }},
		{"exclusion_position", "invalid_element_record", func(in *c.RouterSelectionInput) { in.Exclusions[0].Position = "/bad~3" }},
		{"exclusion_template_empty", "invalid_element_record", func(in *c.RouterSelectionInput) { in.Exclusions[0].Source.TemplateID = "" }},
		{"exclusion_template_utf8", "invalid_element_record", func(in *c.RouterSelectionInput) { in.Exclusions[0].Source.TemplateID = badUTF8 }},
		{"exclusion_uuid", "invalid_element_id", func(in *c.RouterSelectionInput) { in.Exclusions[0].Source.ElementID = "invalid-id" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := sharedSelection()
			in.Shared[0].Element.Content = `{`
			in.Local = []c.LocalContent{{ID: selectionLocalID, Content: json.RawMessage(`{`), Position: "/local/0"}}
			in.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{Source: selectionSource(in), Mode: c.ContentModeVariables}, Position: "/overrides/0"}}
			in.Exclusions = []c.SharedExclusion{{Source: selectionSource(in), Position: "/exclusions/0"}}
			tc.edit(&in)
			issue := selectionFailure(t, in, tc.code)
			if issue.ElementID == "invalid-id" || issue.Location.Position == "not-pointer" || issue.Location.Owner.ID == badUTF8 {
				t.Fatalf("unsafe diagnostic: %+v", issue)
			}
		})
	}
	for _, owner := range []c.Owner{{}, {Kind: "router", ID: "wrong-kind"}} {
		in := selectionInput()
		in.Template = owner
		selectionFailure(t, in, "invalid_element_record")
	}
}

func TestSelectionUnknownExcludedMode(t *testing.T) {
	in := sharedSelection()
	in.Shared[0].Element.Content = `{`
	in.Shared = append(in.Shared, c.SharedElement{Element: c.Element{ID: selectionLocalID, Content: `{}`}, Position: "/outbounds/1"})
	source := c.ElementSource{TemplateID: in.Template.ID, ElementID: selectionLocalID}
	in.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{Source: source, Mode: "unknown"}, Position: "/overrides/0"}}
	in.Exclusions = []c.SharedExclusion{{Source: source, Position: "/exclusions/0"}}
	issue := selectionFailure(t, in, "invalid_element_record")
	if issue.Location != (c.ElementLocation{Owner: in.Router, Position: "/overrides/0"}) {
		t.Fatalf("location: %+v", issue)
	}
}

func TestSelectionBindingSources(t *testing.T) {
	for _, kind := range []string{"override", "exclusion"} {
		for _, where := range []string{"foreign_existing", "foreign_missing", "missing", "local_only"} {
			t.Run(kind+"/"+where, func(t *testing.T) {
				in := sharedSelection()
				in.Shared[0].Element.Content = `{`
				source := selectionSource(in)
				code := "source_not_found"
				switch where {
				case "foreign_existing":
					source.TemplateID = "template-b"
					code = "source_mismatch"
				case "foreign_missing":
					source.TemplateID = "template-b"
					source.ElementID = selectionLocalID
					code = "source_mismatch"
				case "missing":
					source.ElementID = selectionLocalID
				case "local_only":
					source.ElementID = selectionLocalID
					in.Local = []c.LocalContent{{ID: selectionLocalID, Content: json.RawMessage(`{}`), Position: "/local/0"}}
				}
				position := "/exclusions/0"
				if kind == "override" {
					position = "/overrides/0"
					in.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{Source: source, Mode: c.ContentModeVariables, Values: map[string]json.RawMessage{"port": json.RawMessage(`"retained"`)}}, Position: position}}
				} else {
					in.Exclusions = []c.SharedExclusion{{Source: source, Position: position}}
				}
				issue := selectionFailure(t, in, code)
				if issue.ElementID != source.ElementID || issue.Location != (c.ElementLocation{Owner: in.Router, Position: position}) || issue.Source != "record" {
					t.Fatalf("bad source error: %+v", issue)
				}
				if kind == "override" && string(in.Overrides[0].Override.Values["port"]) != `"retained"` {
					t.Fatal("lost values")
				}
			})
		}
	}
}

func TestSelectionDuplicateBindings(t *testing.T) {
	for _, kind := range []string{"override", "exclusion"} {
		t.Run(kind, func(t *testing.T) {
			in := sharedSelection()
			in.Shared[0].Element.Content = `{`
			source := selectionSource(in)
			in.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{Source: source, Mode: c.ContentModeVariables}, Position: "/overrides/0"}}
			in.Exclusions = []c.SharedExclusion{{Source: source, Position: "/exclusions/0"}}
			code, first, second := "duplicate_override", "/overrides/0", "/overrides/1"
			if kind == "override" {
				copy := in.Overrides[0]
				copy.Position = second
				in.Overrides = append(in.Overrides, copy)
			} else {
				code, first, second = "duplicate_exclusion", "/exclusions/0", "/exclusions/1"
				copy := in.Exclusions[0]
				copy.Position = second
				in.Exclusions = append(in.Exclusions, copy)
			}
			issue := selectionFailure(t, in, code)
			if issue.Location != (c.ElementLocation{Owner: in.Router, Position: second}) || issue.OtherLocation == nil || *issue.OtherLocation != (c.ElementLocation{Owner: in.Router, Position: first}) {
				t.Fatalf("both positions: %+v", issue)
			}
		})
	}
}

func TestSelectionExclusionWithOverride(t *testing.T) {
	in := sharedSelection()
	in.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{Source: selectionSource(in), Mode: c.ContentModeReplacement, Content: json.RawMessage(`{"literal":null}`), Values: map[string]json.RawMessage{"port": json.RawMessage(`"retained"`)}}, Position: "/overrides/0"}}
	in.Exclusions = []c.SharedExclusion{{Source: selectionSource(in), Position: "/exclusions/0"}}
	if got := selected(t, in); len(got.Elements) != 0 {
		t.Fatalf("excluded element returned: %+v", got.Elements)
	}
	if in.Overrides[0].Override.Mode != c.ContentModeReplacement || string(in.Overrides[0].Override.Values["port"]) != `"retained"` {
		t.Fatal("changed hidden state")
	}
}

func TestSelectionExcludedErrors(t *testing.T) {
	for _, kind := range []string{"template", "definition", "default", "value", "missing", "replacement"} {
		t.Run(kind, func(t *testing.T) {
			in := sharedSelection()
			code := "invalid_json"
			in.Exclusions = []c.SharedExclusion{{Source: selectionSource(in), Position: "/exclusions/0"}}
			switch kind {
			case "template":
				in.Shared[0].Element.Content = `{`
			case "definition":
				in.Shared[0].Element.Variables[0].Type = "invalid"
				code = "invalid_variable_type"
			case "default":
				in.Shared[0].Element.Variables[0].Default = json.RawMessage(`{`)
			case "missing":
				in.Shared[0].Element.Variables[0].Default = nil
				code = "missing_value"
			case "value":
				in.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{Source: selectionSource(in), Mode: c.ContentModeVariables, Values: map[string]json.RawMessage{"port": nil}}, Position: "/overrides/0"}}
			case "replacement":
				in.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{Source: selectionSource(in), Mode: c.ContentModeReplacement, Content: json.RawMessage(`{`), Values: map[string]json.RawMessage{"port": nil}}, Position: "/overrides/0"}}
			}
			before := fmt.Sprintf("%#v", in)
			if got := selected(t, in); len(got.Elements) != 0 {
				t.Fatal("excluded content returned")
			}
			in.Local = []c.LocalContent{{ID: selectionLocalID, Content: json.RawMessage(`{"x":null}`), Position: "/local/0"}}
			if got := selected(t, in); len(got.Elements) != 1 || got.Elements[selectionLocalID].Origin != "local" {
				t.Fatal("local lost")
			}
			in.Local = nil
			if fmt.Sprintf("%#v", in) != before {
				t.Fatal("changed excluded state")
			}
			in.Exclusions = nil
			selectionFailure(t, in, code)
		})
	}
}

func TestSelectionRepeatedExclusion(t *testing.T) {
	in := sharedSelection()
	in.Exclusions = []c.SharedExclusion{{Source: selectionSource(in), Position: "/exclusions/0"}}
	in.Local = []c.LocalContent{{ID: selectionLocalID, Content: json.RawMessage(`{"local":true}`), Position: "/local/0"}}
	first := selected(t, in)
	in.Shared[0].Element.Content = `{"port":"${port}","extension":true}`
	second := selected(t, in)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("template edit changed excluded/local result")
	}
	other := in
	other.Router.ID = "router-b"
	other.Exclusions = nil
	got := selected(t, other)
	if len(got.Elements) != 2 || string(got.Elements[selectionID].Content) != `{"extension":true,"port":443}` {
		t.Fatal("other router did not inherit current content")
	}
}

func TestSelectionRemoveExclusion(t *testing.T) {
	for _, mode := range []c.ContentMode{c.ContentModeVariables, c.ContentModeReplacement} {
		t.Run(string(mode), func(t *testing.T) {
			in := sharedSelection()
			in.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{Source: selectionSource(in), Mode: mode, Content: json.RawMessage(`{"port":9443}`), Values: map[string]json.RawMessage{"port": json.RawMessage(`8443`)}}, Position: "/overrides/0"}}
			in.Exclusions = []c.SharedExclusion{{Source: selectionSource(in), Position: "/exclusions/0"}}
			if got := selected(t, in); len(got.Elements) != 0 {
				t.Fatal("not excluded")
			}
			in.Shared[0].Element.Content = `{"port":"${port}","extension":true}`
			in.Exclusions = nil
			got := selected(t, in).Elements[selectionID]
			want := `{"extension":true,"port":8443}`
			if mode == c.ContentModeReplacement {
				want = `{"port":9443}`
			}
			if string(got.Content) != want || got.Mode != mode || got.Source == nil || *got.Source != selectionSource(in) {
				t.Fatalf("bad restored mode: %+v", got)
			}
		})
	}
}

func TestSelectionResetWhileExcluded(t *testing.T) {
	for _, required := range []bool{false, true} {
		t.Run(fmt.Sprint(required), func(t *testing.T) {
			in := sharedSelection()
			current := c.ContentOverride{Source: selectionSource(in), Mode: c.ContentModeReplacement, Content: json.RawMessage(`{"port":9443}`), Values: map[string]json.RawMessage{"port": json.RawMessage(`8443`)}}
			in.Exclusions = []c.SharedExclusion{{Source: selectionSource(in), Position: "/exclusions/0"}}
			in.Overrides = []c.LocatedContentOverride{{Override: current, Position: "/overrides/0"}}
			selected(t, in)
			candidate, issues := c.TransitionContentOverride(selectionID, c.ElementLocation{Owner: in.Template, Position: in.Shared[0].Position}, c.ElementLocation{Owner: in.Router, Position: "/overrides/0"}, &current, c.ContentAction{Kind: c.ResetContentOverride})
			if candidate != nil || len(issues) != 0 {
				t.Fatalf("reset: %+v, %+v", candidate, issues)
			}
			in.Overrides = nil
			in.Shared[0].Element.Variables[0].Default = json.RawMessage(`7443`)
			if required {
				in.Shared[0].Element.Variables[0].Default = nil
			}
			if got := selected(t, in); len(got.Elements) != 0 {
				t.Fatal("reset removed exclusion")
			}
			in.Exclusions = nil
			if required {
				issue := selectionFailure(t, in, "missing_value")
				if issue.Location != (c.ElementLocation{Owner: in.Router, Position: "/shared/0"}) {
					t.Fatalf("missing position: %+v", issue)
				}
			} else {
				got := selected(t, in).Elements[selectionID]
				if string(got.Content) != `{"port":7443}` || got.Mode != c.ContentModeVariables {
					t.Fatalf("not reset: %+v", got)
				}
			}
			if in.Overrides != nil {
				t.Fatal("values resurrected")
			}
		})
	}
}

const selectionValueID = "550e8400-e29b-41d4-a716-446655440012"
const selectionReplaceID = "550e8400-e29b-41d4-a716-446655440013"
const selectionSecondLocalID = "550e8400-e29b-41d4-a716-446655440014"

func mixedSelection() c.RouterSelectionInput {
	in := sharedSelection()
	personal := in.Shared[0]
	personal.Element.ID = selectionValueID
	personal.Position = "/outbounds/1"
	replacement := c.SharedElement{Element: c.Element{ID: selectionReplaceID, Content: `{`}, Position: "/outbounds/2"}
	in.Shared = append(in.Shared, personal, replacement)
	raw := json.RawMessage(` {"n":1} `)
	in.Overrides = []c.LocatedContentOverride{
		{Override: c.ContentOverride{Source: c.ElementSource{TemplateID: in.Template.ID, ElementID: selectionValueID}, Mode: c.ContentModeVariables, Values: map[string]json.RawMessage{"port": json.RawMessage(`8443`)}}, Position: "/overrides/0"},
		{Override: c.ContentOverride{Source: c.ElementSource{TemplateID: in.Template.ID, ElementID: selectionReplaceID}, Mode: c.ContentModeReplacement, Content: raw}, Position: "/overrides/1"},
	}
	// Deliberately shared backing storage must not alias any result.
	in.Local = []c.LocalContent{{ID: selectionLocalID, Content: raw, Position: "/local/0"}, {ID: selectionSecondLocalID, Content: raw, Position: "/local/1"}}
	return in
}

func TestSelectionMixedAndIndependent(t *testing.T) {
	in := mixedSelection()
	beforeInput := fmt.Sprintf("%#v", in)
	got := selected(t, in)
	if len(got.Elements) != 5 || string(got.Elements[selectionID].Content) != `{"port":443}` || string(got.Elements[selectionValueID].Content) != `{"port":8443}` || got.Elements[selectionReplaceID].Mode != c.ContentModeReplacement || got.Elements[selectionLocalID].Origin != "local" {
		t.Fatalf("bad mixed set: %+v", got.Elements)
	}
	if fmt.Sprintf("%#v", in) != beforeInput {
		t.Fatal("selection changed inputs")
	}
	beforeResult, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	in.Local[0].Content[1] = 'x'
	in.Overrides[0].Override.Values["port"][0] = '9'
	in.Overrides[1].Override.Source.TemplateID = "template-changed"
	in.Shared[0].Element.Variables[0].Default[0] = '7'
	afterResult, err := json.Marshal(got)
	if err != nil || string(beforeResult) != string(afterResult) {
		t.Fatal("result aliases mutable input")
	}
	currentInput := fmt.Sprintf("%#v", in)
	neighbor := string(got.Elements[selectionSecondLocalID].Content)
	replacement := string(got.Elements[selectionReplaceID].Content)
	got.Elements[selectionLocalID].Content[1] = 'y'
	got.Elements[selectionValueID].Content[0] = '!'
	got.Elements[selectionValueID].Source.TemplateID = "changed-result-source"
	if fmt.Sprintf("%#v", in) != currentInput {
		t.Fatal("result mutation changed input")
	}
	if string(got.Elements[selectionSecondLocalID].Content) != neighbor || string(got.Elements[selectionReplaceID].Content) != replacement || got.Elements[selectionID].Source.TemplateID != "template-a" || got.Elements[selectionReplaceID].Source.TemplateID != "template-a" {
		t.Fatal("results alias one another")
	}
}

func TestSelectionAtomicDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		kind, code, source, position, path, expected, name string
		offset                                             bool
	}{
		{"template", "unsupported_interpolation", "template", "/outbounds/0", "/address", "", "port", false},
		{"definition", "invalid_variable_type", "definition", "/outbounds/0", "/0/type", "", "port", false},
		{"default", "type_mismatch", "default", "/outbounds/0", "", "number", "port", false},
		{"value", "type_mismatch", "value", "/overrides/0", "", "number", "port", false},
		{"missing", "missing_value", "value", "/shared/1", "", "", "port", false},
		{"unknown", "unknown_variable", "value", "/overrides/0", "", "", "extra", false},
		{"replacement", "invalid_json", "content", "/overrides/0", "", "", "", true},
		{"local", "duplicate_json_key", "content", "/local/0", "/a~1b~0c", "", "", false},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			in := sharedSelection()
			in.Overrides = []c.LocatedContentOverride{{Override: c.ContentOverride{Source: selectionSource(in), Mode: c.ContentModeVariables, Values: map[string]json.RawMessage{"port": json.RawMessage(`443`)}}, Position: "/overrides/0"}}
			switch tc.kind {
			case "template":
				in.Shared[0].Element.Content = `{"address":"demo-do-not-disclose-${port}"}`
			case "definition":
				in.Shared[0].Element.Variables[0].Type = "demo-do-not-disclose"
			case "default":
				in.Shared[0].Element.Variables[0].Default = json.RawMessage(`"demo-do-not-disclose"`)
			case "value":
				in.Overrides[0].Override.Values["port"] = json.RawMessage(`"demo-do-not-disclose"`)
			case "missing":
				in.Overrides = nil
				in.Shared[0].Element.Variables[0].Default = nil
			case "unknown":
				in.Overrides[0].Override.Values["extra"] = json.RawMessage(`"demo-do-not-disclose"`)
			case "replacement":
				in.Overrides[0].Override.Mode = c.ContentModeReplacement
				in.Overrides[0].Override.Content = json.RawMessage(`{"demo-do-not-disclose":`)
			case "local":
				in.Local = []c.LocalContent{{ID: selectionLocalID, Content: json.RawMessage(`{"a/b~c":"demo-do-not-disclose","a/b~c":2}`), Position: "/local/0"}}
			}
			// The failure follows a valid shared record; partial results must be discarded.
			in.Shared = append([]c.SharedElement{{Element: c.Element{ID: selectionValueID, Content: `{"good":true}`}, Position: "/routing/rules/0"}}, in.Shared...)
			before := fmt.Sprintf("%#v", in)
			issue := selectionFailure(t, in, tc.code)
			owner := in.Router
			id := selectionID
			if tc.source == "template" || tc.source == "definition" || tc.source == "default" {
				owner = in.Template
			}
			if tc.kind == "local" {
				id = selectionLocalID
			}
			if issue.Location != (c.ElementLocation{Owner: owner, Position: tc.position}) || issue.ElementID != id || issue.Source != tc.source || issue.ExpectedType != tc.expected {
				t.Fatalf("diagnostic context: %+v", issue)
			}
			if tc.name != "" && (issue.VariableName == nil || *issue.VariableName != tc.name) {
				t.Fatalf("variable name: %+v", issue)
			}
			if tc.offset {
				if issue.Offset == nil || *issue.Offset < 0 {
					t.Fatalf("offset lost: %+v", issue)
				}
			} else if tc.code != "missing_value" && tc.code != "unknown_variable" {
				if issue.Path == nil || *issue.Path != tc.path {
					t.Fatalf("path lost: %+v", issue)
				}
			}
			if issue.DefinitionIndex != nil {
				t.Fatal("invented definition index; B01 reports its definition through Path")
			}
			diagnostic, _ := json.Marshal(issue)
			if strings.Contains(string(diagnostic), "demo-do-not-disclose") {
				t.Fatal("diagnostic disclosed content")
			}
			if fmt.Sprintf("%#v", in) != before {
				t.Fatal("failure changed input")
			}
		})
	}
}

func TestSelectionUnorderedAndLiteralFlags(t *testing.T) {
	in := mixedSelection()
	in.Shared[0].Element.Content = `{"enabled":false,"_xkeenUiId":"literal-data"}`
	first := selected(t, in)
	in.Shared[0], in.Shared[2] = in.Shared[2], in.Shared[0]
	in.Local[0], in.Local[1] = in.Local[1], in.Local[0]
	second := selected(t, in)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("input order changed content set")
	}
	if string(second.Elements[selectionID].Content) != `{"_xkeenUiId":"literal-data","enabled":false}` {
		t.Fatal("interpreted metadata/flags in JSON")
	}
	if strings.Contains(string(second.Elements[selectionValueID].Content), "_xkeenUiId") {
		t.Fatal("injected identity marker")
	}
}
