package elementjson_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration/elementjson"
)

const id = "550e8400-e29b-41d4-a716-446655440000"

var location = configuration.ElementLocation{Owner: configuration.Owner{Kind: "template", ID: "template-a"}, Position: "/outbounds/0"}

func roundTrip(t *testing.T, input configuration.Element) *configuration.Element {
	t.Helper()
	raw, issues := elementjson.Encode(input, location, nil)
	if raw == nil || len(issues) != 0 {
		t.Fatalf("encode: %s %+v", raw, issues)
	}
	got, issues := elementjson.Decode(raw, location)
	if got == nil || len(issues) != 0 {
		t.Fatalf("decode: %+v %+v", got, issues)
	}
	if got.ID != input.ID || got.Content != input.Content {
		t.Fatalf("round trip changed identity or source: %+v", got)
	}
	return got
}

func TestExactSourceRoundTrip(t *testing.T) {
	content := " \n{ \"z\":1e+03, \"a\":\"\\u0061\", \"custom\":[9007199254740993,false] }\r\n"
	roundTrip(t, configuration.Element{ID: id, Content: content})
}

func TestDefinitionRoundTrip(t *testing.T) {
	in := configuration.Element{ID: id, Content: `{}`, Variables: []configuration.VariableDefinition{
		{Name: "text", Type: "string", Default: []byte(`""`)},
		{Name: "number", Type: "number", Default: []byte(`9007199254740993`)},
		{Name: "fraction", Type: "number", Default: []byte(`0.12345678901234567890123456789`)},
		{Name: "huge", Type: "number", Default: []byte(`1e400`)},
		{Name: "flag", Type: "boolean", Default: []byte(`false`)},
		{Name: "object", Type: "object", Default: []byte(`{}`)},
		{Name: "array", Type: "array", Default: []byte(`[]`)},
		{Name: "absent", Type: "string"},
		{Name: "null", Type: "string", Default: []byte(`null`)},
	}}
	got := roundTrip(t, in)
	if len(got.Variables) != len(in.Variables) {
		t.Fatalf("lost definitions: %+v", got.Variables)
	}
	for i, want := range in.Variables {
		v := got.Variables[i]
		if v.Name != want.Name || v.Type != want.Type || string(v.Default) != string(want.Default) || (v.Default == nil) != (want.Default == nil) {
			t.Fatalf("definition %d: %+v; want %+v", i, v, want)
		}
	}
}

func TestStoredContentShapes(t *testing.T) {
	for _, source := range []string{`[]`, `"${host}"`, `42`, `false`, `null`, `{"tag":"primary-proxy","_xkeenUiId":"user-text","extension":{}}`} {
		t.Run(source, func(t *testing.T) { roundTrip(t, configuration.Element{ID: id, Content: source}) })
	}
}

func TestRecordIntegrity(t *testing.T) {
	for _, tc := range []struct{ name, raw, code string }{
		{"missing_id", `{"content":"{}","variables":[]}`, "invalid_element_id"},
		{"missing_content", `{"id":"` + id + `","variables":[]}`, "invalid_element_record"},
		{"content_null", `{"id":"` + id + `","content":null,"variables":[]}`, "invalid_element_record"},
		{"missing_variables", `{"id":"` + id + `","content":"{}"}`, "invalid_element_record"},
		{"variables_null", `{"id":"` + id + `","content":"{}","variables":null}`, "invalid_element_record"},
		{"variables_object", `{"id":"` + id + `","content":"{}","variables":{}}`, "invalid_element_record"},
		{"unknown", `{"id":"` + id + `","content":"{}","variables":[],"extra":true}`, "invalid_element_record"},
		{"case", `{"ID":"` + id + `","content":"{}","variables":[]}`, "invalid_element_record"},
		{"missing_name", `{"id":"` + id + `","content":"{}","variables":[{"type":"string"}]}`, "invalid_element_record"},
		{"null_name", `{"id":"` + id + `","content":"{}","variables":[{"name":null,"type":"string"}]}`, "invalid_element_record"},
		{"numeric_type", `{"id":"` + id + `","content":"{}","variables":[{"name":"a","type":1}]}`, "invalid_element_record"},
		{"extra_definition", `{"id":"` + id + `","content":"{}","variables":[{"name":"a","type":"string","extra":1}]}`, "invalid_element_record"},
		{"duplicate", `{"id":"` + id + `","content":"{}","content":"[]","variables":[]}`, "duplicate_json_key"},
		{"escaped_duplicate", `{"id":"` + id + `","content":"{}","\u0063ontent":"[]","variables":[]}`, "duplicate_json_key"},
		{"duplicate_default", `{"id":"` + id + `","content":"{}","variables":[{"name":"a","type":"object","default":{"a":1,"a":2}}]}`, "duplicate_json_key"},
		{"second_root", `{"id":"` + id + `","content":"{}","variables":[]} true`, "invalid_json"},
		{"array", `[]`, "invalid_element_record"},
		{"null", `null`, "invalid_element_record"},
		{"invalid_utf8", string([]byte{34, 255, 34}), "invalid_json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, issues := elementjson.Decode([]byte(tc.raw), location)
			if got != nil || len(issues) == 0 || issues[0].Code != tc.code {
				t.Fatalf("decode = %+v, %+v; want %s", got, issues, tc.code)
			}
		})
	}
}

func TestDecodeDoesNotDiscover(t *testing.T) {
	raw := []byte(`{"id":"` + id + `","content":"\"${host}\"","variables":[]}`)
	read, issues := elementjson.Decode(raw, location)
	if read == nil || len(issues) != 0 || len(read.Variables) != 0 {
		t.Fatalf("decode: %+v %+v", read, issues)
	}
	saved := roundTrip(t, *read)
	if len(saved.Variables) != 1 || saved.Variables[0].Name != "host" || saved.Variables[0].Type != "string" || string(saved.Variables[0].Default) != `""` {
		t.Fatalf("missing declaration: %+v", saved)
	}
	if len(read.Variables) != 0 {
		t.Fatal("changed restored record")
	}
}

func TestDraftStorage(t *testing.T) {
	for _, content := range []string{`{"optional":null,"url":"https://${host}/path"}`, `"${}"`, `{"first":{"a":1},"second":{"a":2}}`, `{}`} {
		in := configuration.Element{ID: id, Content: content, Variables: []configuration.VariableDefinition{
			{Name: "absent", Type: "string"}, {Name: "empty", Type: "number", Default: []byte(`""`)},
			{Name: "bad name", Type: "integer", Default: []byte(`null`)},
			{Name: "duplicate", Type: "number", Default: []byte(`"443"`)},
			{Name: "duplicate", Type: "object", Default: []byte(`[null]`)},
		}}
		got := roundTrip(t, in)
		if len(got.Variables) != len(in.Variables) {
			t.Fatalf("definitions changed: %+v", got.Variables)
		}
		for i, v := range in.Variables {
			if got.Variables[i].Name != v.Name || got.Variables[i].Type != v.Type || string(got.Variables[i].Default) != string(v.Default) {
				t.Fatalf("repaired definition %d", i)
			}
		}
	}
}

func TestStorageSafeDiagnosticsAndIsolation(t *testing.T) {
	for _, raw := range []string{
		`{"id":"demo-private-value","content":"{}","variables":[]}`,
		`{"id":"` + id + `","content":"{\"secret\":\"demo-private-value\",}","variables":[]}`,
		`{"id":"` + id + `","content":"{}","variables":[{"name":"a","type":"string","unknown":"demo-private-value"}]}`,
		`{"id":"` + id + `","content":"{}","variables":[{"name":"a","type":"object","default":{"a":"demo-private-value","a":2}}]}`,
	} {
		input := []byte(raw)
		before := bytes.Clone(input)
		got, issues := elementjson.Decode(input, location)
		if got != nil || len(issues) == 0 {
			t.Fatal("accepted invalid record")
		}
		if strings.Contains(fmt.Sprintf("%+v", issues), "demo-private-value") {
			t.Fatal("leaked value")
		}
		if !bytes.Equal(input, before) {
			t.Fatal("changed input record")
		}
	}
	in := configuration.Element{ID: id, Content: `{"secret":"demo-private-value"}`, Variables: []configuration.VariableDefinition{{Name: "a", Type: "object", Default: []byte(`{"x":1}`)}}}
	raw, issues := elementjson.Encode(in, location, nil)
	if raw == nil || len(issues) != 0 {
		t.Fatal(issues)
	}
	saved := bytes.Clone(raw)
	got, issues := elementjson.Decode(raw, location)
	if got == nil || len(issues) != 0 {
		t.Fatal(issues)
	}
	got.Variables[0].Name = "changed"
	got.Variables[0].Default[5] = '2'
	if !bytes.Equal(raw, saved) || in.Variables[0].Name != "a" || string(in.Variables[0].Default) != `{"x":1}` {
		t.Fatal("aliased storage inputs")
	}
	raw[0] = 'x'
	if got.ID != id || got.Content != in.Content {
		t.Fatal("read aliases record buffer")
	}
	// Failure while preparing a later default leaves existing definitions intact.
	in.Variables = append(in.Variables, configuration.VariableDefinition{Name: "bad", Type: "object", Default: []byte(`{"secret":"demo-private-value",}`)})
	raw, issues = elementjson.Encode(in, location, nil)
	if raw != nil || len(issues) == 0 || issues[0].Source != "default" || issues[0].DefinitionIndex == nil || *issues[0].DefinitionIndex != 1 {
		t.Fatalf("bad default: %s %+v", raw, issues)
	}
	if strings.Contains(fmt.Sprintf("%+v", issues), "demo-private-value") {
		t.Fatal("leaked default")
	}
}

func TestKnownIdentityInRecordError(t *testing.T) {
	raw := []byte(`{"id":"` + id + `","content":null,"variables":[]}`)
	got, issues := elementjson.Decode(raw, location)
	if got != nil || len(issues) == 0 || issues[0].ElementID != id {
		t.Fatalf("known UUID missing: %+v %+v", got, issues)
	}
}

func TestElementEditingLifecycle(t *testing.T) {
	initial := "{\r\n  \"port\": \"${port}\", \"custom\": 1e+03\r\n}\n"
	input := configuration.Element{ID: id, Content: initial}
	first := roundTrip(t, input)
	if len(first.Variables) != 1 || first.Variables[0].Name != "port" {
		t.Fatal("not declared")
	}
	first.Variables[0].Type = "number"
	first.Variables[0].Default = []byte(`443`)
	original := first.ID
	raw, issues := elementjson.Encode(*first, location, &original)
	if raw == nil || len(issues) != 0 {
		t.Fatal(issues)
	}
	second, issues := elementjson.Decode(raw, location)
	if second == nil || len(issues) != 0 {
		t.Fatal(issues)
	}
	second.Content = " {\"port\": 8443, \"custom\": 1e+03} \n"
	prepared, issues := configuration.PrepareElement(*second, location, &original)
	if prepared == nil || len(issues) != 0 {
		t.Fatal(issues)
	}
	refs := []configuration.ElementRef{{ID: prepared.ID, Position: location.Position}}
	if issues := configuration.ValidateTemplateIDs(location.Owner, refs); len(issues) != 0 {
		t.Fatal(issues)
	}
	raw, issues = elementjson.Encode(*prepared, location, &original)
	if raw == nil || len(issues) != 0 {
		t.Fatal(issues)
	}
	final, issues := elementjson.Decode(raw, location)
	if final == nil || len(issues) != 0 || final.ID != id || final.Content != second.Content || len(final.Variables) != 1 {
		t.Fatalf("final: %+v %+v", final, issues)
	}
	if final.Variables[0].Name != "port" || final.Variables[0].Type != "number" || string(final.Variables[0].Default) != `443` {
		t.Fatal("lost settings after removing reference")
	}
	if first.Content != initial || input.Content != initial || len(input.Variables) != 0 {
		t.Fatal("changed earlier states")
	}
}

func TestDraftRoundTripBeforeAssembly(t *testing.T) {
	for _, tc := range []struct{ content, code string }{
		{`{"optional":null}`, "null_not_allowed"},
		{`{"address":"https://${host}/path"}`, "unsupported_interpolation"},
		{`{"address":"${host}"}`, "missing_value"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			input := configuration.Element{ID: id, Content: tc.content}
			got := roundTrip(t, input)
			before, errors := elementjson.Encode(*got, location, &input.ID)
			if len(errors) != 0 {
				t.Fatal(errors)
			}
			result, issues := configuration.AssembleElement(got.ID, []byte(got.Content), got.Variables, nil)
			if result != nil || len(issues) == 0 || issues[0].Code != tc.code {
				t.Fatalf("expected %s without result: %s %+v", tc.code, result, issues)
			}
			after, errors := elementjson.Encode(*got, location, &input.ID)
			if len(errors) != 0 || !bytes.Equal(before, after) {
				t.Fatal("assembly changed stored draft")
			}
		})
	}
}
