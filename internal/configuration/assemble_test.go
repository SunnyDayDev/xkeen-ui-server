package configuration_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestAssembleElement(t *testing.T) {
	defs := []configuration.VariableDefinition{
		{Name: "server.address", Type: "string"},
		{Name: "port", Type: "number", Default: json.RawMessage(`443`)},
	}
	got, issues := configuration.AssembleElement("element-demo", json.RawMessage(`{"address":"${server.address}","port":"${port}","literal":"$${port}"}`), defs, map[string]json.RawMessage{"server.address": json.RawMessage(`"edge.example.com"`)})
	assertResult(t, got, issues, `{"address":"edge.example.com","port":443,"literal":"${port}"}`)
}

func TestAssemblePreparationErrors(t *testing.T) {
	for _, tc := range []struct {
		name                                       string
		defs                                       []configuration.VariableDefinition
		personal                                   map[string]json.RawMessage
		code, source, variable, path, expectedType string
		hasPath                                    bool
	}{
		{name: "unused_required", defs: []configuration.VariableDefinition{{Name: "host", Type: "string"}}, code: "missing_value", source: "value", variable: "host"},
		{name: "wrong_personal", defs: []configuration.VariableDefinition{{Name: "port", Type: "number", Default: json.RawMessage(`443`)}}, personal: map[string]json.RawMessage{"port": json.RawMessage(`"8443"`)}, code: "type_mismatch", source: "value", variable: "port", expectedType: "number", hasPath: true},
		{name: "unselected_default", defs: []configuration.VariableDefinition{{Name: "port", Type: "number", Default: json.RawMessage(`"443"`)}}, personal: map[string]json.RawMessage{"port": json.RawMessage(`8443`)}, code: "type_mismatch", source: "default", variable: "port", expectedType: "number", hasPath: true},
		{name: "invalid_name", defs: []configuration.VariableDefinition{{Name: "bad name", Type: "string"}}, code: "invalid_variable_name", source: "definition", variable: "bad name", path: "/0/name", hasPath: true},
		{name: "invalid_type", defs: []configuration.VariableDefinition{{Name: "x", Type: "integer"}}, code: "invalid_variable_type", source: "definition", variable: "x", path: "/0/type", hasPath: true},
		{name: "duplicate", defs: []configuration.VariableDefinition{{Name: "x", Type: "string"}, {Name: "x", Type: "string"}}, code: "duplicate_variable", source: "definition", variable: "x", path: "/1/name", hasPath: true},
		{name: "extra_personal", personal: map[string]json.RawMessage{"other": json.RawMessage(`true`)}, code: "unknown_variable", source: "value", variable: "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, issues := configuration.AssembleElement("element-demo", json.RawMessage(`{}`), tc.defs, tc.personal)
			issue := requireIssue(t, got, issues, tc.code, tc.source)
			if issue.VariableName == nil || *issue.VariableName != tc.variable || issue.ExpectedType != tc.expectedType || issue.Offset != nil {
				t.Fatalf("wrong diagnostic: %+v", issue)
			}
			if tc.hasPath {
				if issue.Path == nil || *issue.Path != tc.path {
					t.Fatalf("wrong path: %+v", issue)
				}
			} else if issue.Path != nil {
				t.Fatalf("unexpected path: %+v", issue)
			}
		})
	}
}

func TestAssembleDefaultsAndExactNames(t *testing.T) {
	for _, personal := range []map[string]json.RawMessage{nil, {"port": json.RawMessage(`" \t\u00a0"`)}} {
		got, issues := configuration.AssembleElement("element-demo", json.RawMessage(`"${port}"`), []configuration.VariableDefinition{{Name: "port", Type: "number", Default: json.RawMessage(`443`)}}, personal)
		assertResult(t, got, issues, `443`)
	}
	defs := []configuration.VariableDefinition{
		{Name: "server.address", Type: "string", Default: json.RawMessage(`"  edge.example.com  "`)},
		{Name: "Host", Type: "string", Default: json.RawMessage(`"upper"`)},
		{Name: "host", Type: "string", Default: json.RawMessage(`"lower"`)},
		{Name: "сервер.адрес", Type: "string", Default: json.RawMessage(`"unicode"`)},
		{Name: "café", Type: "string", Default: json.RawMessage(`"composed"`)},
		{Name: "café", Type: "string", Default: json.RawMessage(`"decomposed"`)},
	}
	got, issues := configuration.AssembleElement("element-demo", json.RawMessage(`["${server.address}","${Host}","${host}","${сервер.адрес}","${café}","${café}"]`), defs, nil)
	assertResult(t, got, issues, `["  edge.example.com  ","upper","lower","unicode","composed","decomposed"]`)
}

func TestAssembleStrictInputs(t *testing.T) {
	for _, tc := range []struct {
		raw, code, path string
		offset          int64
	}{
		{"", "invalid_json", "", 0}, {`{`, "invalid_json", "", 1},
		{`true false`, "invalid_json", "", 5}, {string([]byte{255}), "invalid_json", "", 0},
		{`{"a":1,"\u0061":2}`, "duplicate_json_key", "/a", -1},
		{`null`, "null_not_allowed", "", -1}, {`{"a/b~c":[null]}`, "null_not_allowed", "/a~1b~0c/0", -1},
	} {
		for _, source := range []string{"template", "value", "default"} {
			t.Run(source+tc.raw, func(t *testing.T) {
				template := json.RawMessage(`{}`)
				var defs []configuration.VariableDefinition
				var personal map[string]json.RawMessage
				if source == "template" {
					template = json.RawMessage(tc.raw)
				} else {
					defs = []configuration.VariableDefinition{{Name: "unused", Type: "object"}}
					if source == "value" {
						personal = map[string]json.RawMessage{"unused": json.RawMessage(tc.raw)}
					} else {
						defs[0].Default = json.RawMessage(tc.raw)
						personal = map[string]json.RawMessage{"unused": json.RawMessage(`{}`)}
					}
				}
				got, issues := configuration.AssembleElement("element-demo", template, defs, personal)
				issue := requireIssue(t, got, issues, tc.code, source)
				if source == "template" {
					if issue.VariableName != nil {
						t.Fatalf("unexpected name: %+v", issue)
					}
				} else if issue.VariableName == nil || *issue.VariableName != "unused" {
					t.Fatalf("missing name: %+v", issue)
				}
				if tc.offset >= 0 {
					if issue.Offset == nil || *issue.Offset != tc.offset || issue.Path != nil {
						t.Fatalf("wrong offset: %+v", issue)
					}
				} else if issue.Path == nil || *issue.Path != tc.path || issue.Offset != nil {
					t.Fatalf("wrong path: %+v", issue)
				}
			})
		}
	}
	defs := []configuration.VariableDefinition{{Name: "x", Type: "string", Default: json.RawMessage(`"fallback"`)}}
	got, issues := configuration.AssembleElement("element-demo", json.RawMessage(`"${x}"`), defs, map[string]json.RawMessage{"x": nil})
	requireIssue(t, got, issues, "invalid_json", "value")
	got, issues = configuration.AssembleElement("element-demo", json.RawMessage(`"${x}"`), defs, nil)
	assertResult(t, got, issues, `"fallback"`)
}

func TestAssembleTemplateContract(t *testing.T) {
	defs := []configuration.VariableDefinition{}
	for _, tc := range []struct{ raw, code, path string }{
		{`{"address":"${host}"}`, "unknown_variable", "/address"},
		{`"${host}${port}"`, "unsupported_interpolation", ""},
		{`{"a/b~c":["${}"]}`, "invalid_reference", "/a~1b~0c/0"},
		{`"$$${host}"`, "unsupported_interpolation", ""},
	} {
		got, issues := configuration.AssembleElement("element-demo", json.RawMessage(tc.raw), defs, nil)
		issue := requireIssue(t, got, issues, tc.code, "template")
		if len(defs) != 0 || issue.Path == nil || *issue.Path != tc.path {
			t.Fatalf("wrong diagnostic/declarations: %+v %+v", issue, defs)
		}
		if tc.code == "unknown_variable" && (issue.VariableName == nil || *issue.VariableName != "host") {
			t.Fatalf("wrong name: %+v", issue)
		}
	}
	for _, host := range []string{`"one.example.com"`, `"two.example.com"`} {
		got, issues := configuration.AssembleElement("element-demo", json.RawMessage(`"${host}"`), []configuration.VariableDefinition{{Name: "host", Type: "string"}}, map[string]json.RawMessage{"host": json.RawMessage(host)})
		assertResult(t, got, issues, host)
	}
}

func TestAssemblyLiteralData(t *testing.T) {
	for _, tc := range []struct{ typ, raw string }{
		{"string", `"${other}"`}, {"string", `"$${other}"`},
		{"object", `{"items":["$$","${broken","https://${other}/path"]}`},
		{"array", `["${other}","$$",{"${key}":"${}"}]`},
	} {
		for _, source := range []string{"value", "default"} {
			t.Run(tc.typ+tc.raw+source, func(t *testing.T) {
				def := configuration.VariableDefinition{Name: "data", Type: tc.typ}
				personal := map[string]json.RawMessage{}
				if source == "value" {
					personal["data"] = json.RawMessage(tc.raw)
				} else {
					def.Default = json.RawMessage(tc.raw)
				}
				got, issues := configuration.AssembleElement("element-demo", json.RawMessage(`"${data}"`), []configuration.VariableDefinition{def}, personal)
				assertResult(t, got, issues, tc.raw)
				got, issues = configuration.SubstituteValues("element-demo", json.RawMessage(`"${data}"`), map[string]json.RawMessage{"data": json.RawMessage(tc.raw)})
				assertResult(t, got, issues, tc.raw)
			})
		}
	}
	got, issues := configuration.AssembleElement("element-demo", json.RawMessage(`{"text":"${text}","options":"${options}"}`), []configuration.VariableDefinition{{Name: "text", Type: "string"}, {Name: "options", Type: "object"}}, map[string]json.RawMessage{"text": json.RawMessage(`"${other}"`), "options": json.RawMessage(`{"items":["$$","${broken","https://${other}/path"]}`)})
	assertResult(t, got, issues, `{"text":"${other}","options":{"items":["$$","${broken","https://${other}/path"]}}`)
}

func TestAssembleTypesAndNumbers(t *testing.T) {
	for _, tc := range []struct{ typ, raw string }{
		{"string", `"443"`}, {"number", `0`}, {"number", `9007199254740993`},
		{"number", `0.12345678901234567890123456789`}, {"number", `1e400`},
		{"boolean", `false`}, {"object", `{}`}, {"array", `[]`},
	} {
		got, issues := configuration.AssembleElement("element-demo", json.RawMessage(`"${x}"`), []configuration.VariableDefinition{{Name: "x", Type: tc.typ}}, map[string]json.RawMessage{"x": json.RawMessage(tc.raw)})
		if tc.typ == "number" {
			if len(issues) != 0 || !equalNumber(string(got), tc.raw) {
				t.Fatalf("inexact number: %s %+v", got, issues)
			}
		} else {
			assertResult(t, got, issues, tc.raw)
		}
	}
	const raw = `{"extension":[0,false,"",{},[],1e400]}`
	got, issues := configuration.AssembleElement("element-demo", json.RawMessage(raw), nil, nil)
	assertResult(t, got, issues, raw)
	got, issues = configuration.AssembleElement("element-demo", json.RawMessage(`{"custom":{"items":["${x}","fixed","${x}"]},"extension":true}`), []configuration.VariableDefinition{{Name: "x", Type: "number", Default: json.RawMessage(`9007199254740993`)}}, nil)
	assertResult(t, got, issues, `{"custom":{"items":[9007199254740993,"fixed",9007199254740993]},"extension":true}`)
}

func TestAssembleInputOwnership(t *testing.T) {
	for _, failure := range []bool{false, true} {
		template := json.RawMessage(`["${personal}","${default}","$${host}"]`)
		if failure {
			template = json.RawMessage(`["${personal}","${default}","${demo-bad name}"]`)
		}
		defs := []configuration.VariableDefinition{{Name: "personal", Type: "object"}, {Name: "default", Type: "array", Default: json.RawMessage(`["demo-private-default"]`)}}
		personal := map[string]json.RawMessage{"personal": json.RawMessage(`{"text":"demo-private-value"}`)}
		snapshot := func() []byte {
			raw, err := json.Marshal(struct {
				Template json.RawMessage
				Defs     []configuration.VariableDefinition
				Personal map[string]json.RawMessage
			}{template, defs, personal})
			if err != nil {
				t.Fatal(err)
			}
			return raw
		}
		before := snapshot()
		got, issues := configuration.AssembleElement("element-demo", template, defs, personal)
		if !bytes.Equal(before, snapshot()) {
			t.Fatal("assembly mutated inputs")
		}
		if failure {
			requireIssue(t, got, issues, "invalid_reference", "template")
			encoded, _ := json.Marshal(issues)
			for _, secret := range []string{"demo-private-default", "demo-private-value", "demo-bad name"} {
				if strings.Contains(string(encoded), secret) {
					t.Fatalf("diagnostic leaked data: %s", encoded)
				}
			}
		} else {
			assertResult(t, got, issues, `[{"text":"demo-private-value"},["demo-private-default"],"${host}"]`)
			for i := range got {
				got[i] = 'x'
			}
			if !bytes.Equal(before, snapshot()) {
				t.Fatal("result aliases input")
			}
		}
	}
}
