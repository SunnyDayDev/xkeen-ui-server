package configuration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestConstantTemplate(t *testing.T) {
	for _, values := range []map[string]json.RawMessage{nil, {}} {
		input := `{"custom":[0,false,"fixed",{},[]]}`
		got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(input), values)
		assertResult(t, got, issues, input)
	}
}

func TestPreparedUnicodeAndWhitespace(t *testing.T) {
	got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(" \n[\"${сервер.адрес}\",\"${café}\",\"${café}\"]\t\r\n"), map[string]json.RawMessage{
		"сервер.адрес": json.RawMessage(`"  edge.example.com  "`),
		"café":         json.RawMessage(`"composed"`), "café": json.RawMessage(`"decomposed"`),
	})
	assertResult(t, got, issues, `["  edge.example.com  ","composed","decomposed"]`)
}

func TestRootTypes(t *testing.T) {
	for _, raw := range []string{`"443"`, `0`, `false`, `{}`, `[]`} {
		t.Run(raw, func(t *testing.T) {
			got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(`"${value}"`), map[string]json.RawMessage{"value": json.RawMessage(raw)})
			assertResult(t, got, issues, raw)
		})
	}
}

func TestObjectTypes(t *testing.T) {
	input := `{"host":"${host}","port":"${port}","enabled":"${enabled}","options":"${options}","targets":"${targets}"}`
	values := map[string]json.RawMessage{
		"host": json.RawMessage(`"edge.example.com"`), "port": json.RawMessage(`443`),
		"enabled": json.RawMessage(`false`), "options": json.RawMessage(`{"network":"tcp"}`),
		"targets": json.RawMessage(`["example.net"]`),
	}
	got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(input), values)
	assertResult(t, got, issues, `{"host":"edge.example.com","port":443,"enabled":false,"options":{"network":"tcp"},"targets":["example.net"]}`)
}

func TestNestedValues(t *testing.T) {
	got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(`{"custom":{"items":[{"port":"${port}"},"fixed","${port}"]},"extension":true}`), map[string]json.RawMessage{"port": json.RawMessage(`443`)})
	assertResult(t, got, issues, `{"custom":{"items":[{"port":443},"fixed",443]},"extension":true}`)
}

func TestExactNames(t *testing.T) {
	for _, host := range []string{`"one.example.com"`, `"two.example.com"`} {
		got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(`{"address":"${server.address}"}`), map[string]json.RawMessage{"server.address": json.RawMessage(host)})
		assertResult(t, got, issues, `{"address":`+host+`}`)
	}
	for _, name := range []string{"server", "Server.Address"} {
		t.Run(name, func(t *testing.T) {
			got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(`"${server.address}"`), map[string]json.RawMessage{name: json.RawMessage(`{"address":"one.example.com"}`)})
			if got != nil || len(issues) == 0 || issues[0].Code != "unknown_variable" {
				t.Fatalf("expected unknown_variable and no result, got %s, %+v", got, issues)
			}
		})
	}
}

func TestExactNumbers(t *testing.T) {
	for _, n := range []string{"9007199254740993", "0.12345678901234567890123456789", "1e400"} {
		t.Run(n, func(t *testing.T) {
			got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(`{"constant":`+n+`,"selected":"${n}"}`), map[string]json.RawMessage{"n": json.RawMessage(n)})
			if len(issues) != 0 {
				t.Fatalf("issues: %+v", issues)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(got, &fields); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"constant", "selected"} {
				if !equalNumber(string(fields[field]), n) {
					t.Fatalf("%s = %s, want exact %s", field, fields[field], n)
				}
			}
		})
	}
	if equalNumber("9007199254740992", "9007199254740993") || equalNumber("0.12345678901234568", "0.12345678901234567890123456789") {
		t.Fatal("numeric comparison missed rounding")
	}
	if !equalNumber("1e3", "1000") {
		t.Fatal("numeric comparison depends on spelling")
	}
}

func equalNumber(a, b string) bool {
	x, okX := new(big.Rat).SetString(a)
	y, okY := new(big.Rat).SetString(b)
	return okX && okY && x.Cmp(y) == 0
}

func TestInvalidJSON(t *testing.T) {
	cases := []struct {
		name, raw string
		offset    int64
	}{
		{"empty", "", 0}, {"unfinished", `{"a":`, 5},
		{"syntax", `[1,]`, 3}, {"second_root", `true false`, 5},
		{"trailing_text", `{} garbage`, 3},
		{"invalid_utf8", string([]byte{'"', 0xff, '"'}), 1},
	}
	for _, tc := range cases {
		for _, source := range []string{"template", "value", "unused_value"} {
			t.Run(tc.name+"/"+source, func(t *testing.T) {
				template := json.RawMessage(tc.raw)
				var values map[string]json.RawMessage
				wantSource := "template"
				if source != "template" {
					wantSource = "value"
					template = json.RawMessage(`"${options}"`)
					if source == "unused_value" {
						template = json.RawMessage(`{}`)
					}
					values = map[string]json.RawMessage{"options": json.RawMessage(tc.raw)}
				}
				got, issues := configuration.SubstituteValues("element-demo", template, values)
				issue := requireIssue(t, got, issues, "invalid_json", wantSource)
				if issue.Offset == nil || *issue.Offset != tc.offset || issue.Path != nil {
					t.Fatalf("expected offset %d without path, issue = %+v", tc.offset, issue)
				}
				if wantSource == "value" && (issue.VariableName == nil || *issue.VariableName != "options") {
					t.Fatalf("missing value name: %+v", issue)
				}
			})
		}
	}
}

func TestDuplicateKeys(t *testing.T) {
	for _, tc := range []struct{ raw, path string }{
		{`{"a":1,"a":2}`, "/a"},
		{`{"nested":{"a":1,"\u0061":2}}`, "/nested/a"},
		{`[{"a/b~c":1,"a/b~c":2}]`, "/0/a~1b~0c"},
	} {
		for _, source := range []string{"template", "value"} {
			t.Run(source+tc.path, func(t *testing.T) {
				template := json.RawMessage(tc.raw)
				var values map[string]json.RawMessage
				if source == "value" {
					template = json.RawMessage(`{}`)
					values = map[string]json.RawMessage{"options": json.RawMessage(tc.raw)}
				}
				got, issues := configuration.SubstituteValues("element-demo", template, values)
				issue := requireIssue(t, got, issues, "duplicate_json_key", source)
				if issue.Path == nil || *issue.Path != tc.path || issue.Offset != nil {
					t.Fatalf("expected path %q, got %+v", tc.path, issue)
				}
				if source == "value" && (issue.VariableName == nil || *issue.VariableName != "options") {
					t.Fatal("missing value name")
				}
			})
		}
	}
}

func TestSeparateObjectKeys(t *testing.T) {
	input := `{"first":{"a":1},"second":{"a":2}}`
	got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(input), nil)
	assertResult(t, got, issues, input)
}

func TestUnknownLocation(t *testing.T) {
	for _, tc := range []struct{ template, path string }{
		{`{"settings":{"address":"${missing}"}}`, "/settings/address"},
		{`"${missing}"`, ""}, {`{"a/b~c":["${missing}"]}`, "/a~1b~0c/0"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(tc.template), nil)
			issue := requireIssue(t, got, issues, "unknown_variable", "template")
			if issue.VariableName == nil || *issue.VariableName != "missing" || issue.Path == nil || *issue.Path != tc.path || issue.Offset != nil {
				t.Fatalf("expected missing at %q, got %+v", tc.path, issue)
			}
		})
	}
}

func TestInputsAndDiagnostics(t *testing.T) {
	const secret = "demo-private-value"
	for _, tc := range []struct{ name, template, value, code string }{
		{"root_success", `"${known}"`, `{"text":"` + secret + `"}`, ""},
		{"nested_success", `{"list":["${known}","${known}"]}`, `"` + secret + `"`, ""},
		{"unknown_after_known", `["${known}","${missing}"]`, `"` + secret + `"`, "unknown_variable"},
		{"invalid_template", `{"private":"` + secret + `",}`, `false`, "invalid_json"},
		{"invalid_value", `"${known}"`, `{"private":"` + secret + `",}`, "invalid_json"},
		{"duplicate_value", `{}`, `{"private":"` + secret + `","private":true}`, "duplicate_json_key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			template := json.RawMessage(tc.template)
			values := map[string]json.RawMessage{"known": json.RawMessage(tc.value), "unused": json.RawMessage(`{"extra":[1,2]}`)}
			beforeTemplate := bytes.Clone(template)
			beforeValues := map[string]json.RawMessage{}
			for name, raw := range values {
				beforeValues[name] = bytes.Clone(raw)
			}
			got, issues := configuration.SubstituteValues("element-demo", template, values)
			if tc.code == "" {
				if len(issues) != 0 || !json.Valid(got) {
					t.Fatalf("expected success: %+v", issues)
				}
				for i := range got {
					got[i] = 'x'
				}
			} else {
				if got != nil || len(issues) == 0 || issues[0].Code != tc.code {
					t.Fatalf("expected %s and no result, got %s, %+v", tc.code, got, issues)
				}
				encoded, err := json.Marshal(issues)
				if err != nil {
					t.Fatal(err)
				}
				for _, diagnostic := range []string{string(encoded), fmt.Sprint(issues), fmt.Sprintf("%+v", issues), fmt.Sprintf("%#v", issues)} {
					if strings.Contains(diagnostic, secret) || strings.Contains(diagnostic, tc.template) {
						t.Fatalf("diagnostic disclosed input: %s", diagnostic)
					}
				}
			}
			if !bytes.Equal(template, beforeTemplate) || !reflect.DeepEqual(values, beforeValues) {
				t.Fatal("input data changed")
			}
		})
	}
}

func requireIssue(t *testing.T, got json.RawMessage, issues []configuration.Issue, code, source string) configuration.Issue {
	t.Helper()
	if got != nil || len(issues) == 0 {
		t.Fatalf("expected %s without result, got %s and %+v", code, got, issues)
	}
	for _, issue := range issues {
		if issue.Code == code && issue.Source == source && issue.ElementID == "element-demo" {
			return issue
		}
	}
	t.Fatalf("missing %s from %s: %+v", code, source, issues)
	return configuration.Issue{}
}

func assertResult(t *testing.T, got json.RawMessage, issues []configuration.Issue, want string) {
	t.Helper()
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	if !json.Valid(got) {
		t.Fatalf("result is not one valid JSON value: %q", got)
	}
	decode := func(raw string) any {
		t.Helper()
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()
		var value any
		if err := dec.Decode(&value); err != nil {
			t.Fatalf("invalid result JSON: %v", err)
		}
		return value
	}
	if !reflect.DeepEqual(decode(string(got)), decode(want)) {
		t.Fatalf("result = %s, want %s", got, want)
	}
}
