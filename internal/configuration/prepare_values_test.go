package configuration_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration/elementjson"
)

func TestPrepareEmpty(t *testing.T) {
	for _, defs := range [][]configuration.VariableDefinition{nil, {}} {
		for _, personal := range []map[string]json.RawMessage{nil, {}} {
			got, issues := configuration.PrepareValues("element-demo", defs, personal)
			if got == nil || len(got) != 0 || len(issues) != 0 {
				t.Fatalf("want empty success, got %v, %+v", got, issues)
			}
		}
	}
}

func requirePreparationIssue(t *testing.T, defs []configuration.VariableDefinition, personal map[string]json.RawMessage, code, source, name string) configuration.Issue {
	t.Helper()
	got, issues := configuration.PrepareValues("element-demo", defs, personal)
	if got != nil || len(issues) == 0 {
		t.Fatalf("want %s and nil result, got %v %+v", code, got, issues)
	}
	issue := issues[0]
	if issue.Code != code || issue.Source != source || issue.ElementID != "element-demo" {
		t.Fatalf("wrong issue: %+v", issue)
	}
	if issue.VariableName == nil || *issue.VariableName != name {
		t.Fatalf("wrong name: %+v", issue)
	}
	return issue
}

func TestPrepareInvalidDefinitions(t *testing.T) {
	for _, name := range []string{"", "server address", "a\t", "a\u00a0b", "a\x00", "a$", "a{", "a}", string([]byte{255})} {
		t.Run(name, func(t *testing.T) {
			got, issues := configuration.PrepareValues("element-demo", []configuration.VariableDefinition{{Name: name, Type: "string"}}, nil)
			if got != nil || len(issues) == 0 {
				t.Fatal("expected invalid_variable_name")
			}
			issue := issues[0]
			if issue.Code != "invalid_variable_name" || issue.Source != "definition" || issue.Path == nil || *issue.Path != "/0/name" || issue.ElementID != "element-demo" {
				t.Fatalf("bad issue %+v", issue)
			}
		})
	}
	for _, typ := range []string{"", "integer", "String", " number", string([]byte{255})} {
		issue := requirePreparationIssue(t, []configuration.VariableDefinition{{Name: "host", Type: typ}}, nil, "invalid_variable_type", "definition", "host")
		if issue.Path == nil || *issue.Path != "/0/type" {
			t.Fatalf("bad path %+v", issue)
		}
	}
	defs := []configuration.VariableDefinition{{Name: "host", Type: "string"}, {Name: "host", Type: "string"}}
	issue := requirePreparationIssue(t, defs, nil, "duplicate_variable", "definition", "host")
	if issue.Path == nil || *issue.Path != "/1/name" {
		t.Fatalf("bad path %+v", issue)
	}
}

func TestPrepareExactNames(t *testing.T) {
	for _, name := range []string{"server", "Server.Address", "other"} {
		requirePreparationIssue(t, []configuration.VariableDefinition{{Name: "server.address", Type: "string", Default: json.RawMessage(`"edge.example.com"`)}}, map[string]json.RawMessage{name: json.RawMessage(`"other.example.com"`)}, "unknown_variable", "value", name)
	}
	names := []string{"server.address", "сервер.адрес", "café", "café", "host-name_1"}
	for _, host := range []string{`"one.example.com"`, `"two.example.com"`} {
		defs := []configuration.VariableDefinition{}
		for _, name := range names {
			defs = append(defs, configuration.VariableDefinition{Name: name, Type: "string", Default: json.RawMessage(host)})
		}
		personal := map[string]json.RawMessage{"café": json.RawMessage(`"personal.example.com"`)}
		got, issues := configuration.PrepareValues("element-demo", defs, personal)
		if len(issues) != 0 || len(got) != len(names) {
			t.Fatalf("bad result %v %+v", got, issues)
		}
		for _, name := range names {
			want := host
			if name == "café" {
				want = `"personal.example.com"`
			}
			if string(got[name]) != want {
				t.Fatalf("%s = %s want %s", name, got[name], want)
			}
		}
	}
}

func TestPrepareMissingAndDefaults(t *testing.T) {
	for _, typ := range []string{"string", "number", "boolean", "object", "array"} {
		for _, empty := range []json.RawMessage{nil, json.RawMessage(`""`), json.RawMessage(`" \t\n "`), json.RawMessage(`"\u00a0"`)} {
			personal := map[string]json.RawMessage{}
			if empty != nil {
				personal["required"] = empty
			}
			for _, def := range []json.RawMessage{nil, json.RawMessage(`" \t "`)} {
				requirePreparationIssue(t, []configuration.VariableDefinition{{Name: "required", Type: typ, Default: def}}, personal, "missing_value", "value", "required")
			}
		}
	}
	for _, empty := range []json.RawMessage{nil, json.RawMessage(`""`), json.RawMessage(`" \t\n "`), json.RawMessage(`"\u00a0"`)} {
		personal := map[string]json.RawMessage{}
		if empty != nil {
			personal["port"] = empty
		}
		got, issues := configuration.PrepareValues("element-demo", []configuration.VariableDefinition{{Name: "port", Type: "number", Default: json.RawMessage(`443`)}}, personal)
		if len(issues) != 0 || string(got["port"]) != "443" {
			t.Fatalf("bad default %v %+v", got, issues)
		}
	}
}

func TestPrepareFilledValues(t *testing.T) {
	for _, tc := range []struct{ typ, raw string }{
		{"string", `"  edge.example.com  "`}, {"string", `"\u200b"`},
		{"boolean", `false`}, {"number", `0`}, {"object", `{}`}, {"array", `[]`}, {"object", `{"label":"   "}`},
	} {
		for _, source := range []string{"value", "default"} {
			t.Run(tc.typ+tc.raw+source, func(t *testing.T) {
				def := configuration.VariableDefinition{Name: "x", Type: tc.typ}
				personal := map[string]json.RawMessage{}
				if source == "default" {
					def.Default = json.RawMessage(tc.raw)
				} else {
					personal["x"] = json.RawMessage(tc.raw)
				}
				got, issues := configuration.PrepareValues("element-demo", []configuration.VariableDefinition{def}, personal)
				if len(issues) != 0 || string(got["x"]) != tc.raw {
					t.Fatalf("changed data %v %+v", got, issues)
				}
			})
		}
	}
}

func TestPrepareTypeMismatch(t *testing.T) {
	types := []string{"string", "number", "boolean", "object", "array"}
	raw := []string{`"8443"`, `8443`, `true`, `{}`, `[]`}
	for i, typ := range types {
		for j, other := range raw {
			if i == j {
				continue
			}
			for _, source := range []string{"value", "default"} {
				t.Run(typ+"/"+types[j]+"/"+source, func(t *testing.T) {
					def := configuration.VariableDefinition{Name: "x", Type: typ, Default: json.RawMessage(raw[i])}
					personal := map[string]json.RawMessage{"x": json.RawMessage(other)}
					if source == "default" {
						def.Default = json.RawMessage(other)
						personal["x"] = json.RawMessage(raw[i])
					}
					issue := requirePreparationIssue(t, []configuration.VariableDefinition{def}, personal, "type_mismatch", source, "x")
					if issue.ExpectedType != typ || issue.Path == nil || *issue.Path != "" {
						t.Fatalf("bad type location %+v", issue)
					}
				})
			}
		}
	}
	got, issues := configuration.PrepareValues("element-demo", []configuration.VariableDefinition{{Name: "port", Type: "number", Default: json.RawMessage(`"   "`)}}, map[string]json.RawMessage{"port": json.RawMessage(`8443`)})
	if len(issues) != 0 || string(got["port"]) != "8443" {
		t.Fatalf("empty default rejected %v %+v", got, issues)
	}
}

func TestPrepareExactData(t *testing.T) {
	for _, tc := range []struct{ typ, raw string }{
		{"number", `9007199254740993`}, {"number", `0.12345678901234567890123456789`}, {"number", `1e400`},
		{"string", `"${other}"`}, {"object", `{"label":"$${other}"}`}, {"array", `["${other}","$$",{"unknown":true}]`},
	} {
		for _, source := range []string{"value", "default"} {
			def := configuration.VariableDefinition{Name: "x", Type: tc.typ}
			personal := map[string]json.RawMessage{}
			if source == "value" {
				personal["x"] = json.RawMessage(tc.raw)
			} else {
				def.Default = json.RawMessage(tc.raw)
			}
			got, issues := configuration.PrepareValues("element-demo", []configuration.VariableDefinition{def}, personal)
			if len(issues) != 0 {
				t.Fatalf("unexpected %+v", issues)
			}
			if tc.typ == "number" {
				if !equalNumber(string(got["x"]), tc.raw) {
					t.Fatal("rounded number")
				}
			} else if string(got["x"]) != tc.raw {
				t.Fatal("reinterpreted data")
			}
		}
	}
}

func TestPrepareStrictJSON(t *testing.T) {
	for _, tc := range []struct {
		raw        json.RawMessage
		code, path string
		offset     int64
	}{
		{nil, "invalid_json", "", 0}, {json.RawMessage{}, "invalid_json", "", 0},
		{json.RawMessage(`{"a":`), "invalid_json", "", 5},
		{json.RawMessage(`true false`), "invalid_json", "", 5},
		{json.RawMessage(`{} garbage`), "invalid_json", "", 3},
		{json.RawMessage{'"', 255, '"'}, "invalid_json", "", 1},
		{json.RawMessage(`{"a":1,"\u0061":2}`), "duplicate_json_key", "/a", 0},
		{json.RawMessage(`[{"a/b~c":1,"a/b~c":2}]`), "duplicate_json_key", "/0/a~1b~0c", 0},
	} {
		for _, source := range []string{"value", "default"} {
			if source == "default" && tc.raw == nil {
				continue
			}
			def := configuration.VariableDefinition{Name: "x", Type: "object", Default: json.RawMessage(`{}`)}
			personal := map[string]json.RawMessage{"x": tc.raw}
			if source == "default" {
				def.Default = tc.raw
				personal["x"] = json.RawMessage(`{}`)
			}
			issue := requirePreparationIssue(t, []configuration.VariableDefinition{def}, personal, tc.code, source, "x")
			if tc.code == "invalid_json" {
				if issue.Offset == nil || *issue.Offset != tc.offset || issue.Path != nil {
					t.Fatalf("bad offset %+v", issue)
				}
			} else if issue.Path == nil || *issue.Path != tc.path || issue.Offset != nil {
				t.Fatalf("bad path %+v", issue)
			}
		}
	}
}

func TestPrepareRootNull(t *testing.T) {
	for _, source := range []string{"value", "default"} {
		def := configuration.VariableDefinition{Name: "host", Type: "string", Default: json.RawMessage(`"edge.example.com"`)}
		personal := map[string]json.RawMessage{"host": json.RawMessage(`null`)}
		if source == "default" {
			def.Default = json.RawMessage(`null`)
			personal["host"] = json.RawMessage(`"edge.example.com"`)
		}
		issue := requirePreparationIssue(t, []configuration.VariableDefinition{def}, personal, "null_not_allowed", source, "host")
		if issue.Path == nil || *issue.Path != "" {
			t.Fatalf("bad null path %+v", issue)
		}
	}
	got, issues := configuration.PrepareValues("element-demo", []configuration.VariableDefinition{{Name: "x", Type: "string"}}, map[string]json.RawMessage{"x": json.RawMessage(`"null"`)})
	if len(issues) != 0 || string(got["x"]) != `"null"` {
		t.Fatal("literal null rejected")
	}
}

func TestPrepareNestedNull(t *testing.T) {
	for _, tc := range []struct{ raw, path string }{
		{`[null]`, "/0"}, {`{"a/b~c":[null]}`, "/a~1b~0c/0"}, {`{"items":[{"nested":[true,null]}]}`, "/items/0/nested/1"},
	} {
		for _, typ := range []string{"object", "array", "string"} {
			for _, source := range []string{"value", "default"} {
				t.Run(tc.path+typ+source, func(t *testing.T) {
					valid := map[string]string{"object": `{}`, "array": `[]`, "string": `"filled"`}[typ]
					def := configuration.VariableDefinition{Name: "options", Type: typ, Default: json.RawMessage(valid)}
					personal := map[string]json.RawMessage{"options": json.RawMessage(tc.raw)}
					if source == "default" {
						def.Default = json.RawMessage(tc.raw)
						personal["options"] = json.RawMessage(valid)
					}
					issue := requirePreparationIssue(t, []configuration.VariableDefinition{def}, personal, "null_not_allowed", source, "options")
					if issue.Path == nil || *issue.Path != tc.path {
						t.Fatalf("bad nested path %+v", issue)
					}
				})
			}
		}
	}
}

func TestPrepareDiagnosticsAndInputs(t *testing.T) {
	const marker = "demo-sensitive-value"
	for _, tc := range []struct{ code, source, raw, typ string }{
		{"missing_value", "value", `" "`, "string"},
		{"type_mismatch", "value", `"demo-sensitive-value"`, "number"},
		{"type_mismatch", "default", `"demo-sensitive-value"`, "number"},
		{"invalid_json", "value", `{"secret":"demo-sensitive-value",}`, "object"},
		{"null_not_allowed", "default", `{"secret":"demo-sensitive-value","optional":null}`, "object"},
	} {
		defs := []configuration.VariableDefinition{{Name: "host", Type: "string", Default: json.RawMessage(`"demo-sensitive-value"`)}, {Name: "bad", Type: tc.typ}}
		personal := map[string]json.RawMessage{"bad": json.RawMessage(tc.raw)}
		if tc.source == "default" {
			defs[1].Default = json.RawMessage(tc.raw)
			delete(personal, "bad")
		}
		savedDefs := clonePreparationDefinitions(defs)
		savedPersonal := map[string]json.RawMessage{}
		for name, raw := range personal {
			savedPersonal[name] = bytes.Clone(raw)
		}
		issue := requirePreparationIssue(t, defs, personal, tc.code, tc.source, "bad")
		diagnostic, err := json.Marshal(issue)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(diagnostic), marker) {
			t.Fatal("data exposed in diagnostic")
		}
		if !reflect.DeepEqual(defs, savedDefs) || !reflect.DeepEqual(personal, savedPersonal) {
			t.Fatal("inputs mutated on failure")
		}
	}
}

func clonePreparationDefinitions(defs []configuration.VariableDefinition) []configuration.VariableDefinition {
	cloned := append([]configuration.VariableDefinition{}, defs...)
	for i := range cloned {
		cloned[i].Default = bytes.Clone(cloned[i].Default)
	}
	return cloned
}

func TestPrepareIndependentBuffers(t *testing.T) {
	for _, direction := range []string{"result_to_input", "input_to_result"} {
		t.Run(direction, func(t *testing.T) {
			shared := json.RawMessage(`{"x":1}`)
			defs := []configuration.VariableDefinition{{Name: "object", Type: "object"}, {Name: "same", Type: "object"}, {Name: "array", Type: "array", Default: json.RawMessage(`[1,2]`)}}
			personal := map[string]json.RawMessage{"object": shared, "same": shared}
			got, issues := configuration.PrepareValues("element-demo", defs, personal)
			if len(issues) != 0 {
				t.Fatal(issues)
			}
			if direction == "result_to_input" {
				got["object"][5] = '9'
				got["array"][1] = '9'
				delete(got, "same")
				if string(shared) != `{"x":1}` || string(defs[2].Default) != `[1,2]` || len(personal) != 2 {
					t.Fatal("result aliases inputs")
				}
			} else {
				shared[5] = '9'
				defs[2].Default[1] = '9'
				delete(personal, "object")
				defs[0].Name = "changed"
				if string(got["object"]) != `{"x":1}` || string(got["same"]) != `{"x":1}` || string(got["array"]) != `[1,2]` {
					t.Fatal("inputs alias result")
				}
			}
		})
	}
	shared := json.RawMessage(`{"x":1}`)
	got, issues := configuration.PrepareValues("element-demo", []configuration.VariableDefinition{{Name: "a", Type: "object"}, {Name: "b", Type: "object"}}, map[string]json.RawMessage{"a": shared, "b": shared})
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	got["a"][5] = '9'
	if string(got["b"]) != `{"x":1}` {
		t.Fatal("result entries alias each other")
	}
}

func TestPrepareThenSubstitute(t *testing.T) {
	defs := []configuration.VariableDefinition{{Name: "server.address", Type: "string", Default: json.RawMessage(`"edge.example.com"`)}, {Name: "port", Type: "number", Default: json.RawMessage(`443`)}}
	values, issues := configuration.PrepareValues(elementUUID, defs, map[string]json.RawMessage{"port": json.RawMessage(`8443`)})
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	got, issues := configuration.SubstituteValues(elementUUID, json.RawMessage(`{"address":"${server.address}","port":"${port}"}`), values)
	assertResult(t, got, issues, `{"address":"edge.example.com","port":8443}`)
}

func TestPrepareStoredDraft(t *testing.T) {
	for _, defaultValue := range []string{`"edge.example.com"`, `null`, `{"nested":[null]}`} {
		typ := "string"
		if strings.HasPrefix(defaultValue, "{") {
			typ = "object"
		}
		input := configuration.Element{ID: elementUUID, Content: `{"optional":null,"address":"https://${host}"}`, Variables: []configuration.VariableDefinition{{Name: "host", Type: typ, Default: json.RawMessage(defaultValue)}}}
		raw, storageIssues := elementjson.Encode(input, elementLocation, nil)
		if len(storageIssues) != 0 {
			t.Fatalf("draft rejected %+v", storageIssues)
		}
		restored, storageIssues := elementjson.Decode(raw, elementLocation)
		if len(storageIssues) != 0 || !reflect.DeepEqual(input, *restored) {
			t.Fatalf("draft changed %+v %+v", restored, storageIssues)
		}
		values, issues := configuration.PrepareValues(restored.ID, restored.Variables, nil)
		if defaultValue == `"edge.example.com"` {
			if len(issues) != 0 || string(values["host"]) != defaultValue {
				t.Fatal("preparation inspected template")
			}
		} else if values != nil || len(issues) == 0 || issues[0].Code != "null_not_allowed" || issues[0].Source != "default" {
			t.Fatalf("null draft assembled %v %+v", values, issues)
		}
		if !reflect.DeepEqual(input, *restored) {
			t.Fatal("preparation changed stored draft")
		}
	}
}

func TestPrepareInvalidUTF8DiagnosticName(t *testing.T) {
	invalid := string([]byte{255})
	for _, source := range []string{"definition", "value"} {
		var defs []configuration.VariableDefinition
		var personal map[string]json.RawMessage
		wantCode := "invalid_variable_name"
		if source == "definition" {
			defs = []configuration.VariableDefinition{{Name: invalid, Type: "string"}}
		} else {
			personal = map[string]json.RawMessage{invalid: json.RawMessage(`"value"`)}
			wantCode = "unknown_variable"
		}
		got, issues := configuration.PrepareValues("element-demo", defs, personal)
		if got != nil || len(issues) == 0 || issues[0].Code != wantCode || issues[0].VariableName != nil {
			t.Fatalf("invalid UTF-8 name retained: %+v", issues)
		}
	}
}
