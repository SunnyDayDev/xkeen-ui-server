package configuration_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

var inheritedRouterLocation = configuration.ElementLocation{Owner: configuration.Owner{Kind: "router", ID: "router-a"}, Position: "/overrides/0"}

func inheritedSource() configuration.ElementSource {
	return configuration.ElementSource{TemplateID: "template-a", ElementID: elementUUID}
}

func inheritedPort() configuration.Element {
	return configuration.Element{ID: elementUUID, Content: `{"port":"${port}"}`, Variables: []configuration.VariableDefinition{{Name: "port", Type: "number", Default: json.RawMessage(`443`)}}}
}

func inheritedValues(raw string) *configuration.VariableOverride {
	return &configuration.VariableOverride{Source: inheritedSource(), Values: map[string]json.RawMessage{"port": json.RawMessage(raw)}}
}

// Snapshot all mutable inputs, including invalid JSON, without parsing them.
func checkInheritedInputs(t *testing.T, element *configuration.Element, personal *configuration.VariableOverride) func() {
	t.Helper()
	before := *element
	if element.Variables != nil {
		before.Variables = append([]configuration.VariableDefinition{}, element.Variables...)
		for i := range before.Variables {
			before.Variables[i].Default = bytes.Clone(element.Variables[i].Default)
		}
	}
	var saved *configuration.VariableOverride
	if personal != nil {
		copy := *personal
		if personal.Values != nil {
			copy.Values = make(map[string]json.RawMessage, len(personal.Values))
			for k, v := range personal.Values {
				copy.Values[k] = bytes.Clone(v)
			}
		}
		saved = &copy
	}
	return func() {
		t.Helper()
		if !reflect.DeepEqual(before, *element) || !reflect.DeepEqual(saved, personal) {
			t.Fatal("assembly mutated inputs")
		}
	}
}

func assertInherited(t *testing.T, got *configuration.InheritedContent, issues []configuration.InheritanceIssue, want string) {
	t.Helper()
	if got == nil || len(issues) != 0 {
		t.Fatalf("got %+v, %+v; want successful content", got, issues)
	}
	if got.Source != inheritedSource() {
		t.Fatalf("wrong source: %+v", got.Source)
	}
	assertResult(t, got.Content, nil, want)
}

func requireInheritedIssue(t *testing.T, got *configuration.InheritedContent, issues []configuration.InheritanceIssue, code string) configuration.InheritanceIssue {
	t.Helper()
	if got != nil || len(issues) != 1 || issues[0].Code != code {
		t.Fatalf("got %+v, %+v; want nil and %s", got, issues, code)
	}
	return issues[0]
}

func TestInheritedDefaults(t *testing.T) {
	for _, tc := range []struct {
		name     string
		personal *configuration.VariableOverride
	}{
		{"absent", nil},
		{"nil_values", &configuration.VariableOverride{Source: inheritedSource()}},
		{"empty_values", &configuration.VariableOverride{Source: inheritedSource(), Values: map[string]json.RawMessage{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			element := inheritedPort()
			defer checkInheritedInputs(t, &element, tc.personal)()
			got, issues := configuration.AssembleInheritedElement(element, elementLocation, inheritedRouterLocation, tc.personal)
			assertInherited(t, got, issues, `{"port":443}`)
		})
	}
}

func TestInheritedInvalidInputs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		change   func(*configuration.Element, *configuration.ElementLocation, *configuration.ElementLocation, *configuration.VariableOverride)
		code     string
		location configuration.ElementLocation
		noID     bool
	}{
		{"source_uuid", func(e *configuration.Element, _, _ *configuration.ElementLocation, _ *configuration.VariableOverride) {
			e.ID = "not-a-uuid"
		}, "invalid_element_id", elementLocation, true},
		{"reference_uuid", func(_ *configuration.Element, _, _ *configuration.ElementLocation, p *configuration.VariableOverride) {
			p.Source.ElementID = "not-a-uuid"
		}, "invalid_element_id", inheritedRouterLocation, false},
		{"reference_template_empty", func(_ *configuration.Element, _, _ *configuration.ElementLocation, p *configuration.VariableOverride) {
			p.Source.TemplateID = ""
		}, "invalid_element_record", inheritedRouterLocation, false},
		{"reference_template_utf8", func(_ *configuration.Element, _, _ *configuration.ElementLocation, p *configuration.VariableOverride) {
			p.Source.TemplateID = string([]byte{255})
		}, "invalid_element_record", inheritedRouterLocation, false},
		{"template_kind", func(_ *configuration.Element, l, _ *configuration.ElementLocation, _ *configuration.VariableOverride) {
			l.Owner.Kind = "router"
		}, "invalid_element_record", configuration.ElementLocation{}, false},
		{"router_kind", func(_ *configuration.Element, _, l *configuration.ElementLocation, _ *configuration.VariableOverride) {
			l.Owner.Kind = "template"
		}, "invalid_element_record", configuration.ElementLocation{}, false},
		{"template_owner_empty", func(_ *configuration.Element, l, _ *configuration.ElementLocation, _ *configuration.VariableOverride) {
			l.Owner.ID = ""
		}, "invalid_element_record", configuration.ElementLocation{}, false},
		{"router_owner_empty", func(_ *configuration.Element, _, l *configuration.ElementLocation, _ *configuration.VariableOverride) {
			l.Owner.ID = ""
		}, "invalid_element_record", configuration.ElementLocation{}, false},
		{"template_owner_utf8", func(_ *configuration.Element, l, _ *configuration.ElementLocation, _ *configuration.VariableOverride) {
			l.Owner.ID = string([]byte{255})
		}, "invalid_element_record", configuration.ElementLocation{}, false},
		{"router_owner_utf8", func(_ *configuration.Element, _, l *configuration.ElementLocation, _ *configuration.VariableOverride) {
			l.Owner.ID = string([]byte{255})
		}, "invalid_element_record", configuration.ElementLocation{}, false},
		{"template_position", func(_ *configuration.Element, l, _ *configuration.ElementLocation, _ *configuration.VariableOverride) {
			l.Position = "invalid-position"
		}, "invalid_element_record", configuration.ElementLocation{}, false},
		{"router_position_escape", func(_ *configuration.Element, _, l *configuration.ElementLocation, _ *configuration.VariableOverride) {
			l.Position = "/bad~2"
		}, "invalid_element_record", configuration.ElementLocation{}, false},
		{"router_position_utf8", func(_ *configuration.Element, _, l *configuration.ElementLocation, _ *configuration.VariableOverride) {
			l.Position = "/" + string([]byte{255})
		}, "invalid_element_record", configuration.ElementLocation{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := inheritedPort()
			tl, rl := elementLocation, inheritedRouterLocation
			p := inheritedValues(`8443`)
			tc.change(&e, &tl, &rl, p)
			defer checkInheritedInputs(t, &e, p)()
			got, issues := configuration.AssembleInheritedElement(e, tl, rl, p)
			issue := requireInheritedIssue(t, got, issues, tc.code)
			id := elementUUID
			if tc.noID {
				id = ""
			}
			want := configuration.InheritanceIssue{Issue: configuration.Issue{Code: tc.code, Source: "record", ElementID: id}, Location: tc.location}
			if !reflect.DeepEqual(issue, want) {
				t.Fatalf("got %+v; want %+v", issue, want)
			}
		})
	}
	t.Run("no_personal_still_checks_context", func(t *testing.T) {
		e := inheritedPort()
		e.Content = `null`
		got, issues := configuration.AssembleInheritedElement(e, elementLocation, configuration.ElementLocation{}, nil)
		requireInheritedIssue(t, got, issues, "invalid_element_record")
	})
	t.Run("invalid_context_and_uuid_are_not_reported", func(t *testing.T) {
		e := inheritedPort()
		e.ID = "not-a-uuid"
		got, issues := configuration.AssembleInheritedElement(e, configuration.ElementLocation{}, inheritedRouterLocation, nil)
		issue := requireInheritedIssue(t, got, issues, "invalid_element_record")
		if !reflect.DeepEqual(issue, configuration.InheritanceIssue{Issue: configuration.Issue{Code: "invalid_element_record", Source: "record"}}) {
			t.Fatalf("unsafe issue: %+v", issue)
		}
	})
}

func TestInheritedSourceMismatch(t *testing.T) {
	e := inheritedPort()
	p := inheritedValues(`8443`)
	p.Source.TemplateID = "template-b"
	defer checkInheritedInputs(t, &e, p)()
	got, issues := configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
	issue := requireInheritedIssue(t, got, issues, "source_mismatch")
	want := configuration.InheritanceIssue{Issue: configuration.Issue{Code: "source_mismatch", Source: "record", ElementID: elementUUID}, Location: inheritedRouterLocation}
	if !reflect.DeepEqual(issue, want) {
		t.Fatalf("wrong source diagnostic: %+v", issue)
	}
}

func TestInheritedElementMismatch(t *testing.T) {
	for _, template := range []string{"template-a", "template-b"} {
		t.Run(template, func(t *testing.T) {
			e := inheritedPort()
			p := inheritedValues(`8443`)
			p.Source = configuration.ElementSource{TemplateID: template, ElementID: otherUUID}
			defer checkInheritedInputs(t, &e, p)()
			got, issues := configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
			issue := requireInheritedIssue(t, got, issues, "element_id_mismatch")
			if issue.ElementID != elementUUID || issue.Location != inheritedRouterLocation || issue.Source != "record" {
				t.Fatalf("wrong identity diagnostic: %+v", issue)
			}
		})
	}
}

func TestInheritedEmptyWrongSource(t *testing.T) {
	for _, values := range []map[string]json.RawMessage{nil, {}} {
		e := inheritedPort()
		p := &configuration.VariableOverride{Source: configuration.ElementSource{TemplateID: "template-b", ElementID: elementUUID}, Values: values}
		check := checkInheritedInputs(t, &e, p)
		got, issues := configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
		requireInheritedIssue(t, got, issues, "source_mismatch")
		check()
	}
}

func TestInheritedSourceBeforeValues(t *testing.T) {
	e := inheritedPort()
	e.Content = `{"port":"prefix${port}"}`
	e.Variables[0].Default = nil
	p := &configuration.VariableOverride{Source: configuration.ElementSource{TemplateID: "template-b", ElementID: elementUUID}}
	defer checkInheritedInputs(t, &e, p)()
	got, issues := configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
	requireInheritedIssue(t, got, issues, "source_mismatch")
}

func TestInheritedTemplateEdit(t *testing.T) {
	e := inheritedPort()
	p := inheritedValues(`8443`)
	got, issues := configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
	assertInherited(t, got, issues, `{"port":8443}`)
	e.Content = `{"port":"${port}","extension":{"enabled":true}}`
	defer checkInheritedInputs(t, &e, p)()
	got, issues = configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
	assertInherited(t, got, issues, `{"port":8443,"extension":{"enabled":true}}`)
}

func TestInheritedDefaultChanges(t *testing.T) {
	for _, tc := range []struct {
		name          string
		p             *configuration.VariableOverride
		first, second string
	}{
		{"absent", nil, `{"port":443}`, `{"port":8443}`},
		{"blank", inheritedValues(`" \t\u00a0"`), `{"port":443}`, `{"port":8443}`},
		{"filled", inheritedValues(`9443`), `{"port":9443}`, `{"port":9443}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := inheritedPort()
			check := checkInheritedInputs(t, &e, tc.p)
			got, issues := configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, tc.p)
			assertInherited(t, got, issues, tc.first)
			check()
			e.Variables[0].Default = json.RawMessage(`8443`)
			defer checkInheritedInputs(t, &e, tc.p)()
			got, issues = configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, tc.p)
			assertInherited(t, got, issues, tc.second)
		})
	}
}

func TestInheritedTypeChange(t *testing.T) {
	e := inheritedPort()
	e.Variables[0].Type = "string"
	e.Variables[0].Default = json.RawMessage(`"443"`)
	p := inheritedValues(`"8443"`)
	got, issues := configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
	assertInherited(t, got, issues, `{"port":"8443"}`)
	e.Variables[0].Type = "number"
	e.Variables[0].Default = json.RawMessage(`443`)
	defer checkInheritedInputs(t, &e, p)()
	got, issues = configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
	issue := requireInheritedIssue(t, got, issues, "type_mismatch")
	if issue.VariableName == nil || *issue.VariableName != "port" || issue.ExpectedType != "number" || issue.Source != "value" {
		t.Fatalf("wrong variable issue: %+v", issue)
	}
}

func TestInheritedRemovedDefinition(t *testing.T) {
	e := configuration.Element{ID: elementUUID, Content: `{"host":"${host}"}`, Variables: []configuration.VariableDefinition{{Name: "host", Type: "string"}}}
	p := &configuration.VariableOverride{Source: inheritedSource(), Values: map[string]json.RawMessage{"host": json.RawMessage(`"edge.example.com"`)}}
	got, issues := configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
	assertInherited(t, got, issues, `{"host":"edge.example.com"}`)
	e.Content = `{}`
	e.Variables = nil
	defer checkInheritedInputs(t, &e, p)()
	got, issues = configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
	issue := requireInheritedIssue(t, got, issues, "unknown_variable")
	if issue.VariableName == nil || *issue.VariableName != "host" || issue.Source != "value" {
		t.Fatalf("wrong variable issue: %+v", issue)
	}
}

func TestInheritedNewRequired(t *testing.T) {
	e := inheritedPort()
	p := inheritedValues(`8443`)
	got, issues := configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
	assertInherited(t, got, issues, `{"port":8443}`)
	e.Variables = append(e.Variables, configuration.VariableDefinition{Name: "clientId", Type: "string"})
	defer checkInheritedInputs(t, &e, p)()
	got, issues = configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
	issue := requireInheritedIssue(t, got, issues, "missing_value")
	if issue.VariableName == nil || *issue.VariableName != "clientId" || issue.Source != "value" {
		t.Fatalf("wrong variable issue: %+v", issue)
	}
}

func TestInheritedDiagnostics(t *testing.T) {
	root, address, definitionPath, offset := "", "/address", "/0/type", int64(1)
	port, host := "port", "host"
	for _, tc := range []struct {
		name     string
		setup    func(*configuration.Element, **configuration.VariableOverride)
		want     configuration.Issue
		location configuration.ElementLocation
	}{
		{"template", func(e *configuration.Element, p **configuration.VariableOverride) {
			e.Content = `{"address":"https://${host}"}`
			e.Variables = []configuration.VariableDefinition{{Name: "host", Type: "string"}}
			*p = &configuration.VariableOverride{Source: inheritedSource(), Values: map[string]json.RawMessage{"host": json.RawMessage(`"PRIVATE_VALUE_MARKER"`)}}
		}, configuration.Issue{Code: "unsupported_interpolation", Source: "template", VariableName: &host, Path: &address}, elementLocation},
		{"default", func(e *configuration.Element, _ **configuration.VariableOverride) {
			e.Variables[0].Default = json.RawMessage(`"PRIVATE_DEFAULT_MARKER"`)
		}, configuration.Issue{Code: "type_mismatch", Source: "default", VariableName: &port, Path: &root, ExpectedType: "number"}, elementLocation},
		{"definition", func(e *configuration.Element, _ **configuration.VariableOverride) {
			e.Variables[0].Type = "PRIVATE_TYPE_MARKER"
		}, configuration.Issue{Code: "invalid_variable_type", Source: "definition", VariableName: &port, Path: &definitionPath}, elementLocation},
		{"json", func(e *configuration.Element, _ **configuration.VariableOverride) { e.Content = `{` }, configuration.Issue{Code: "invalid_json", Source: "template", Offset: &offset}, elementLocation},
		{"value", func(_ *configuration.Element, p **configuration.VariableOverride) {
			*p = inheritedValues(`"PRIVATE_VALUE_MARKER"`)
		}, configuration.Issue{Code: "type_mismatch", Source: "value", VariableName: &port, Path: &root, ExpectedType: "number"}, inheritedRouterLocation},
		{"missing_without_personal", func(e *configuration.Element, p **configuration.VariableOverride) {
			e.Variables[0].Default = nil
			*p = nil
		}, configuration.Issue{Code: "missing_value", Source: "value", VariableName: &port}, inheritedRouterLocation},
		{"removed", func(e *configuration.Element, _ **configuration.VariableOverride) {
			e.Variables = nil
			e.Content = `{}`
		}, configuration.Issue{Code: "unknown_variable", Source: "value", VariableName: &port}, inheritedRouterLocation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := inheritedPort()
			p := inheritedValues(`8443`)
			tc.setup(&e, &p)
			defer checkInheritedInputs(t, &e, p)()
			got, issues := configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
			issue := requireInheritedIssue(t, got, issues, tc.want.Code)
			tc.want.ElementID = elementUUID
			want := configuration.InheritanceIssue{Issue: tc.want, Location: tc.location}
			// Comparing the complete structure also excludes any value/default/raw JSON.
			if !reflect.DeepEqual(issue, want) {
				t.Fatalf("got %+v; want %+v", issue, want)
			}
		})
	}
}

func TestInheritedIndependentContent(t *testing.T) {
	for _, tc := range []struct {
		name, typ, raw string
		personal       bool
	}{
		{"personal_array", "array", `[9007199254740993,false,{"x":"${literal}"},"$$"]`, true},
		{"personal_object", "object", `{"ordered":[2,1],"literal":"$${x}","n":9007199254740993}`, true},
		{"default_array", "array", `[9007199254740993,false,{"x":"${literal}"},"$$"]`, false},
		{"default_object", "object", `{"ordered":[2,1],"literal":"$${x}","n":9007199254740993}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := configuration.Element{ID: elementUUID, Content: `{"extension":"${data}"}`, Variables: []configuration.VariableDefinition{{Name: "data", Type: tc.typ, Default: json.RawMessage(tc.raw)}}}
			var p *configuration.VariableOverride
			if tc.personal {
				p = &configuration.VariableOverride{Source: inheritedSource(), Values: map[string]json.RawMessage{"data": json.RawMessage(tc.raw)}}
			}
			check := checkInheritedInputs(t, &e, p)
			got, issues := configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
			assertInherited(t, got, issues, `{"extension":`+tc.raw+`}`)
			got.Content[0] = 'X'
			check()
			got, issues = configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
			assertInherited(t, got, issues, `{"extension":`+tc.raw+`}`)
			before := bytes.Clone(got.Content)
			e.Variables[0].Default[0] = 'X'
			if p != nil {
				p.Values["data"][0] = 'X'
			}
			if !bytes.Equal(before, got.Content) {
				t.Fatal("result aliases input buffers")
			}
		})
	}
}

func TestInheritedVariableRegressions(t *testing.T) {
	for _, tc := range []struct {
		name         string
		setup        func(*configuration.Element, *configuration.VariableOverride)
		code, source string
	}{
		{"template_null", func(e *configuration.Element, _ *configuration.VariableOverride) { e.Content = `{"nested":[null]}` }, "null_not_allowed", "template"},
		{"value_null", func(_ *configuration.Element, p *configuration.VariableOverride) {
			p.Values["port"] = json.RawMessage(`null`)
		}, "null_not_allowed", "value"},
		{"default_null", func(e *configuration.Element, _ *configuration.VariableOverride) {
			e.Variables[0].Default = json.RawMessage(`null`)
		}, "null_not_allowed", "default"},
		{"invalid_default_with_value", func(e *configuration.Element, _ *configuration.VariableOverride) {
			e.Variables[0].Default = json.RawMessage(`"wrong"`)
		}, "type_mismatch", "default"},
		{"unknown_value", func(_ *configuration.Element, p *configuration.VariableOverride) {
			p.Values["extra"] = json.RawMessage(`true`)
		}, "unknown_variable", "value"},
		{"nil_value", func(_ *configuration.Element, p *configuration.VariableOverride) { p.Values["port"] = nil }, "invalid_json", "value"},
		{"template_duplicate", func(e *configuration.Element, _ *configuration.VariableOverride) { e.Content = `{"x":1,"x":2}` }, "duplicate_json_key", "template"},
		{"value_duplicate", func(_ *configuration.Element, p *configuration.VariableOverride) {
			p.Values["port"] = json.RawMessage(`{"x":1,"x":2}`)
		}, "duplicate_json_key", "value"},
		{"default_duplicate", func(e *configuration.Element, _ *configuration.VariableOverride) {
			e.Variables[0].Default = json.RawMessage(`{"x":1,"x":2}`)
		}, "duplicate_json_key", "default"},
		{"invalid_reference", func(e *configuration.Element, _ *configuration.VariableOverride) { e.Content = `"${}"` }, "invalid_reference", "template"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := inheritedPort()
			p := inheritedValues(`8443`)
			tc.setup(&e, p)
			defer checkInheritedInputs(t, &e, p)()
			got, issues := configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
			issue := requireInheritedIssue(t, got, issues, tc.code)
			location := elementLocation
			if tc.source == "value" {
				location = inheritedRouterLocation
			}
			if issue.Source != tc.source || issue.Location != location || issue.ElementID != elementUUID {
				t.Fatalf("wrong issue: %+v", issue)
			}
		})
	}
	for _, tc := range []struct{ template, want string }{
		{`"${port}"`, `8443`},
		{`["${port}",false]`, `[8443,false]`},
		{`{"literal":"$${port}","once":"$$$$","${key}":"${port}"}`, `{"literal":"${port}","once":"$$","${key}":8443}`},
	} {
		t.Run(tc.template, func(t *testing.T) {
			e := inheritedPort()
			e.Content = tc.template
			p := inheritedValues(`8443`)
			defer checkInheritedInputs(t, &e, p)()
			got, issues := configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
			assertInherited(t, got, issues, tc.want)
		})
	}
}

func TestInheritedCanonicalUUIDs(t *testing.T) {
	for _, id := range []string{"", "00000000-0000-0000-0000-000000000000", "550E8400-E29B-41D4-A716-446655440000", "550e8400-e29b-11d4-a716-446655440000", "550e8400-e29b-41d4-7716-446655440000"} {
		for _, reference := range []bool{false, true} {
			e := inheritedPort()
			p := inheritedValues(`8443`)
			if reference {
				p.Source.ElementID = id
			} else {
				e.ID = id
			}
			got, issues := configuration.AssembleInheritedElement(e, elementLocation, inheritedRouterLocation, p)
			issue := requireInheritedIssue(t, got, issues, "invalid_element_id")
			wantID, location := "", elementLocation
			if reference {
				wantID, location = elementUUID, inheritedRouterLocation
			}
			if issue.ElementID != wantID || issue.Location != location {
				t.Fatalf("wrong identity diagnostic: %+v", issue)
			}
		}
	}
}

func TestInheritedValidLocations(t *testing.T) {
	for _, position := range []string{"", "/a~0b~1c/0"} {
		e := inheritedPort()
		tl, rl := elementLocation, inheritedRouterLocation
		tl.Position, rl.Position = position, position
		got, issues := configuration.AssembleInheritedElement(e, tl, rl, nil)
		assertInherited(t, got, issues, `{"port":443}`)
	}
}
