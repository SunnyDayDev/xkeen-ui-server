package configuration_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestTemplateNull(t *testing.T) {
	for _, tc := range []struct{ raw, path string }{
		{`null`, ""}, {`{"optional":null}`, "/optional"}, {`{"a/b~c":[null]}`, "/a~1b~0c/0"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(tc.raw), nil)
			issue := requireIssue(t, got, issues, "null_not_allowed", "template")
			if issue.Path == nil || *issue.Path != tc.path || issue.VariableName != nil || issue.Offset != nil {
				t.Fatalf("wrong null location: %+v", issue)
			}
		})
	}
	const raw = `{"optional":"null","text":"","constant":false}`
	got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(raw), nil)
	assertResult(t, got, issues, raw)
}

func TestTemplateEscapes(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`"$${host}"`, `"${host}"`},
		{`{"nested":["https://$${host}/path","$$$$","cost $5","$","$${host"]}`, `{"nested":["https://${host}/path","$$","cost $5","$","${host"]}`},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(tc.raw), nil)
			assertResult(t, got, issues, tc.want)
		})
	}
}

func TestTemplateInterpolation(t *testing.T) {
	for _, raw := range []string{`"https://${host}/path"`, `" ${host} "`, `"${host}${port}"`, `"$${literal} ${host}"`, `"$$${host}"`} {
		for _, values := range []map[string]json.RawMessage{nil, {"host": json.RawMessage(`"edge.example.com"`), "port": json.RawMessage(`443`)}} {
			t.Run(raw, func(t *testing.T) {
				got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(raw), values)
				requireIssue(t, got, issues, "unsupported_interpolation", "template")
			})
		}
	}
	got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(`"$$$${host}"`), nil)
	assertResult(t, got, issues, `"$${host}"`)
}

func TestTemplateInvalidReferences(t *testing.T) {
	for _, name := range []string{"", "server address", "a{b", "a$b", "a\tb", "a\x00b"} {
		for _, prefix := range []string{"", "prefix "} {
			raw, _ := json.Marshal(prefix + "${" + name + "}")
			t.Run(string(raw), func(t *testing.T) {
				got, issues := configuration.SubstituteValues("element-demo", raw, map[string]json.RawMessage{name: json.RawMessage(`true`)})
				issue := requireIssue(t, got, issues, "invalid_reference", "template")
				if issue.VariableName != nil {
					t.Fatalf("invalid name disclosed: %+v", issue)
				}
			})
		}
	}
	for _, raw := range []string{`"${host"`, `"prefix ${host"`} {
		got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(raw), nil)
		requireIssue(t, got, issues, "invalid_reference", "template")
	}
}

func TestTemplateDecodedSyntaxAndDiagnostics(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`"\u0024{host}"`, `"edge.example.com"`},
		{`"\u0024\u0024{host}"`, `"${host}"`},
		{`{"${key}":"$${host}","$${key}":"fixed","${broken":"fixed"}`, `{"${key}":"${host}","$${key}":"fixed","${broken":"fixed"}`},
	} {
		got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(tc.raw), map[string]json.RawMessage{"host": json.RawMessage(`"edge.example.com"`)})
		assertResult(t, got, issues, tc.want)
	}
	for _, tc := range []struct{ raw, code, path, name string }{
		{`"${server\u0020address}"`, "invalid_reference", "", ""},
		{`"\\${host}"`, "unsupported_interpolation", "", "host"},
		{`"${host}${port}"`, "unsupported_interpolation", "", ""},
		{`{"a/b~c":["https://${host}/path"]}`, "unsupported_interpolation", "/a~1b~0c/0", "host"},
		{`{"a/b~c":["${demo-bad reference}"]}`, "invalid_reference", "/a~1b~0c/0", ""},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(tc.raw), nil)
			issue := requireIssue(t, got, issues, tc.code, "template")
			if issue.Path == nil || *issue.Path != tc.path || issue.Offset != nil || issue.ExpectedType != "" {
				t.Fatalf("wrong location/type: %+v", issue)
			}
			if tc.name == "" {
				if issue.VariableName != nil {
					t.Fatalf("unexpected name: %q", *issue.VariableName)
				}
			} else if issue.VariableName == nil || *issue.VariableName != tc.name {
				t.Fatalf("wrong name: %+v", issue)
			}
			encoded, _ := json.Marshal(issues)
			if strings.Contains(string(encoded), "demo-bad reference") || strings.Contains(string(encoded), "https://") {
				t.Fatalf("diagnostic leaked text: %s", encoded)
			}
		})
	}
	got, issues := configuration.SubstituteValues("element-demo", json.RawMessage(`"\${host}"`), nil)
	issue := requireIssue(t, got, issues, "invalid_json", "template")
	if issue.Offset == nil || *issue.Offset != 2 || issue.Path != nil {
		t.Fatalf("wrong JSON position: %+v", issue)
	}
}
