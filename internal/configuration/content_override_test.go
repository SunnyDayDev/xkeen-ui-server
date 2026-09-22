package configuration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func ownContent(raw string) *configuration.ContentOverride {
	return &configuration.ContentOverride{Source: inheritedSource(), Mode: configuration.ContentModeReplacement, Content: json.RawMessage(raw)}
}

func assertSelected(t *testing.T, got *configuration.SelectedContent, issues []configuration.InheritanceIssue, mode configuration.ContentMode, want string) {
	t.Helper()
	if got == nil || len(issues) != 0 {
		t.Fatalf("got %+v, %+v; want successful selection", got, issues)
	}
	if got.Source != inheritedSource() || got.Mode != mode {
		t.Fatalf("wrong source or mode: %+v", got)
	}
	assertResult(t, got.Content, nil, want)
}

func TestSelectReplacement(t *testing.T) {
	element := inheritedPort()
	element.Content = `{"tag":"primary-proxy","protocol":"freedom","extension":true}`
	want := `{"tag":"personal-proxy","protocol":"blackhole"}`
	got, issues := configuration.SelectElementContent(element, elementLocation, inheritedRouterLocation, ownContent(want))
	assertSelected(t, got, issues, configuration.ContentModeReplacement, want)
}

func requireSelectionIssue(t *testing.T, got *configuration.SelectedContent, issues []configuration.InheritanceIssue, code string) configuration.InheritanceIssue {
	t.Helper()
	if got != nil || len(issues) != 1 || issues[0].Code != code {
		t.Fatalf("got %+v, %+v; want nil and %s", got, issues, code)
	}
	return issues[0]
}

type contentIdentityCase struct {
	name     string
	change   func(*configuration.Element, *configuration.ElementLocation, *configuration.ElementLocation, *configuration.ContentOverride)
	code     string
	location configuration.ElementLocation
	noID     bool
}

func contentIdentityCases() []contentIdentityCase {
	return []contentIdentityCase{
		{"element_uuid", func(e *configuration.Element, _, _ *configuration.ElementLocation, _ *configuration.ContentOverride) {
			e.ID = "invalid-uuid"
		}, "invalid_element_id", elementLocation, true},
		{"reference_uuid", func(_ *configuration.Element, _, _ *configuration.ElementLocation, p *configuration.ContentOverride) {
			p.Source.ElementID = "invalid-uuid"
		}, "invalid_element_id", inheritedRouterLocation, false},
		{"empty_template", func(_ *configuration.Element, _, _ *configuration.ElementLocation, p *configuration.ContentOverride) {
			p.Source.TemplateID = ""
		}, "invalid_element_record", inheritedRouterLocation, false},
		{"utf8_template", func(_ *configuration.Element, _, _ *configuration.ElementLocation, p *configuration.ContentOverride) {
			p.Source.TemplateID = string([]byte{255})
		}, "invalid_element_record", inheritedRouterLocation, false},
		{"other_element", func(_ *configuration.Element, _, _ *configuration.ElementLocation, p *configuration.ContentOverride) {
			p.Source.ElementID = "550e8400-e29b-41d4-a716-446655440001"
		}, "element_id_mismatch", inheritedRouterLocation, false},
		{"other_template", func(_ *configuration.Element, _, _ *configuration.ElementLocation, p *configuration.ContentOverride) {
			p.Source.TemplateID = "template-b"
		}, "source_mismatch", inheritedRouterLocation, false},
		{"both_mismatch", func(_ *configuration.Element, _, _ *configuration.ElementLocation, p *configuration.ContentOverride) {
			p.Source.TemplateID = "template-b"
			p.Source.ElementID = "550e8400-e29b-41d4-a716-446655440001"
		}, "element_id_mismatch", inheritedRouterLocation, false},
		{"template_kind", func(_ *configuration.Element, l, _ *configuration.ElementLocation, _ *configuration.ContentOverride) {
			l.Owner.Kind = "router"
		}, "invalid_element_record", configuration.ElementLocation{}, false},
		{"router_kind", func(_ *configuration.Element, _, l *configuration.ElementLocation, _ *configuration.ContentOverride) {
			l.Owner.Kind = "template"
		}, "invalid_element_record", configuration.ElementLocation{}, false},
		{"owner_empty", func(_ *configuration.Element, l, _ *configuration.ElementLocation, _ *configuration.ContentOverride) {
			l.Owner.ID = ""
		}, "invalid_element_record", configuration.ElementLocation{}, false},
		{"owner_utf8", func(_ *configuration.Element, _, l *configuration.ElementLocation, _ *configuration.ContentOverride) {
			l.Owner.ID = string([]byte{255})
		}, "invalid_element_record", configuration.ElementLocation{}, false},
		{"position", func(_ *configuration.Element, l, _ *configuration.ElementLocation, _ *configuration.ContentOverride) {
			l.Position = "bad"
		}, "invalid_element_record", configuration.ElementLocation{}, false},
		{"position_escape", func(_ *configuration.Element, _, l *configuration.ElementLocation, _ *configuration.ContentOverride) {
			l.Position = "/bad~2"
		}, "invalid_element_record", configuration.ElementLocation{}, false},
		{"position_utf8", func(_ *configuration.Element, _, l *configuration.ElementLocation, _ *configuration.ContentOverride) {
			l.Position = "/" + string([]byte{255})
		}, "invalid_element_record", configuration.ElementLocation{}, false},
		{"unknown_mode", func(_ *configuration.Element, _, _ *configuration.ElementLocation, p *configuration.ContentOverride) {
			p.Mode = "unknown"
		}, "invalid_element_record", inheritedRouterLocation, false},
		{"empty_mode", func(_ *configuration.Element, _, _ *configuration.ElementLocation, p *configuration.ContentOverride) {
			p.Mode = ""
		}, "invalid_element_record", inheritedRouterLocation, false},
	}
}

func TestSelectIdentity(t *testing.T) {
	for _, tc := range contentIdentityCases() {
		t.Run(tc.name, func(t *testing.T) {
			e := inheritedPort()
			e.Content = `null`
			tl, rl := elementLocation, inheritedRouterLocation
			p := ownContent(`broken`)
			tc.change(&e, &tl, &rl, p)
			got, issues := configuration.SelectElementContent(e, tl, rl, p)
			issue := requireSelectionIssue(t, got, issues, tc.code)
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
}

func TestSelectInherited(t *testing.T) {
	for _, tc := range []struct {
		name     string
		personal *configuration.ContentOverride
		want     string
	}{
		{"absent", nil, `{"port":443}`},
		{"empty", &configuration.ContentOverride{Source: inheritedSource(), Mode: configuration.ContentModeVariables}, `{"port":443}`},
		{"personal", &configuration.ContentOverride{Source: inheritedSource(), Mode: configuration.ContentModeVariables, Values: map[string]json.RawMessage{"port": json.RawMessage(`8443`)}, Content: json.RawMessage(`broken`)}, `{"port":8443}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, issues := configuration.SelectElementContent(inheritedPort(), elementLocation, inheritedRouterLocation, tc.personal)
			assertSelected(t, got, issues, configuration.ContentModeVariables, tc.want)
		})
	}
	for _, raw := range []string{`"wrong"`, `null`, `broken`} {
		p := &configuration.ContentOverride{Source: inheritedSource(), Mode: configuration.ContentModeVariables, Values: map[string]json.RawMessage{"port": json.RawMessage(raw)}}
		want, wi := configuration.AssembleInheritedElement(inheritedPort(), elementLocation, inheritedRouterLocation, &configuration.VariableOverride{Source: p.Source, Values: p.Values})
		got, issues := configuration.SelectElementContent(inheritedPort(), elementLocation, inheritedRouterLocation, p)
		if want != nil || got != nil || !reflect.DeepEqual(issues, wi) {
			t.Fatalf("diagnostic regression: got %+v, %+v; want %+v", got, issues, wi)
		}
	}
}

func TestSelectLiteral(t *testing.T) {
	for _, raw := range []string{`null`, `{"extension":null,"items":[null,{"value":null}]}`, `"${host}"`, `9007199254740993`, `false`, `[null,"$$",0]`, `{"${literal}":["${host}","$$","https://${host}/path",9007199254740993,false],"_xkeenUiId":"literal"}`, `{}`, `[]`, `""`, `0`, `true`} {
		got, issues := configuration.SelectElementContent(inheritedPort(), elementLocation, inheritedRouterLocation, ownContent(raw))
		assertSelected(t, got, issues, configuration.ContentModeReplacement, raw)
	}
	for _, source := range []string{"template", "default", "value"} {
		t.Run(source, func(t *testing.T) {
			e := inheritedPort()
			p := &configuration.ContentOverride{Source: inheritedSource(), Mode: configuration.ContentModeVariables}
			switch source {
			case "template":
				e.Content = `{"x":[null]}`
			case "default":
				e.Variables[0].Default = json.RawMessage(`null`)
			case "value":
				p.Values = map[string]json.RawMessage{"port": json.RawMessage(`null`)}
			}
			got, issues := configuration.SelectElementContent(e, elementLocation, inheritedRouterLocation, p)
			issue := requireSelectionIssue(t, got, issues, "null_not_allowed")
			if issue.Source != source {
				t.Fatalf("wrong null source: %+v", issue)
			}
		})
	}
}

type replacementJSONCase struct {
	name       string
	raw        json.RawMessage
	code, path string
}

func replacementJSONCases() []replacementJSONCase {
	return []replacementJSONCase{
		{"nil", nil, "invalid_json", ""}, {"empty", json.RawMessage{}, "invalid_json", ""},
		{"spaces", json.RawMessage(" \n\t"), "invalid_json", ""}, {"truncated", json.RawMessage(`{"x":`), "invalid_json", ""},
		{"two_roots", json.RawMessage(`{} []`), "invalid_json", ""}, {"utf8", json.RawMessage{'"', 255, '"'}, "invalid_json", ""},
		{"duplicate", json.RawMessage(`{"x":1,"x":2}`), "duplicate_json_key", "/x"},
		{"nested_duplicate", json.RawMessage(`{"items":[{"a":1,"a":2}]}`), "duplicate_json_key", "/items/0/a"},
		{"escaped_duplicate", json.RawMessage(`{"x":1,"\u0078":2}`), "duplicate_json_key", "/x"},
		{"pointer_duplicate", json.RawMessage(`{"a/b~c":1,"a/b~c":2}`), "duplicate_json_key", "/a~1b~0c"},
	}
}
func checkContentJSONIssue(t *testing.T, issue configuration.InheritanceIssue, tc replacementJSONCase) {
	t.Helper()
	if issue.Location != inheritedRouterLocation || issue.ElementID != elementUUID || issue.Source != "content" || issue.Code != tc.code {
		t.Fatalf("wrong diagnostic: %+v", issue)
	}
	if tc.path != "" {
		if issue.Path == nil || *issue.Path != tc.path || issue.Offset != nil {
			t.Fatalf("wrong path: %+v", issue)
		}
	} else {
		if issue.Offset == nil || *issue.Offset < 0 || *issue.Offset > int64(len(tc.raw)) || issue.Path != nil {
			t.Fatalf("wrong offset: %+v", issue)
		}
	}
}
func TestSelectInvalidJSON(t *testing.T) {
	for _, tc := range replacementJSONCases() {
		t.Run(tc.name, func(t *testing.T) {
			p := ownContent("")
			p.Content = tc.raw
			got, issues := configuration.SelectElementContent(inheritedPort(), elementLocation, inheritedRouterLocation, p)
			issue := requireSelectionIssue(t, got, issues, tc.code)
			checkContentJSONIssue(t, issue, tc)
		})
	}
}

func checkContentInputs(t *testing.T, e *configuration.Element, p *configuration.ContentOverride) func() {
	t.Helper()
	var saved *configuration.ContentOverride
	var values *configuration.VariableOverride
	if p != nil {
		copy := *p
		copy.Content = bytes.Clone(p.Content)
		if p.Values != nil {
			copy.Values = make(map[string]json.RawMessage, len(p.Values))
			for k, v := range p.Values {
				copy.Values[k] = bytes.Clone(v)
			}
		}
		saved = &copy
		values = &configuration.VariableOverride{Source: p.Source, Values: p.Values}
	}
	checkElement := checkInheritedInputs(t, e, values)
	return func() {
		t.Helper()
		checkElement()
		if !reflect.DeepEqual(saved, p) {
			t.Fatal("operation mutated personal state")
		}
	}
}

func TestSelectInactive(t *testing.T) {
	e := inheritedPort()
	p := ownContent(`{"protocol":"freedom","extension":null}`)
	p.Values = map[string]json.RawMessage{"port": json.RawMessage(`"wrong"`), "unknown": json.RawMessage(`broken`)}
	for _, change := range []func(){
		func() {},
		func() { e.Content = `{"port":"${port}","new":true}`; e.Variables[0].Default = json.RawMessage(`8443`) },
		func() {
			e.Variables = append(e.Variables, configuration.VariableDefinition{Name: "required", Type: "string"})
		},
		func() { e.Variables[0].Default = json.RawMessage(`broken`) },
		func() { e.Content = `broken` },
		func() { e.Content = `null`; e.Variables[0].Type = "unknown" },
	} {
		change()
		check := checkContentInputs(t, &e, p)
		got, issues := configuration.SelectElementContent(e, elementLocation, inheritedRouterLocation, p)
		assertSelected(t, got, issues, configuration.ContentModeReplacement, `{"protocol":"freedom","extension":null}`)
		check()
	}
}

func TestSelectSafety(t *testing.T) {
	for _, direction := range []string{"input", "output"} {
		t.Run(direction, func(t *testing.T) {
			raw := `{"x":[9007199254740993,null,"${literal}"]}`
			e := inheritedPort()
			p := ownContent(raw)
			check := checkContentInputs(t, &e, p)
			got, issues := configuration.SelectElementContent(e, elementLocation, inheritedRouterLocation, p)
			assertSelected(t, got, issues, configuration.ContentModeReplacement, raw)
			check()
			if direction == "input" {
				p.Content[0] = '['
				if string(got.Content) != raw {
					t.Fatal("input changed result")
				}
			} else {
				got.Content[0] = '['
				if string(p.Content) != raw {
					t.Fatal("result changed input")
				}
			}
		})
	}
	t.Run("diagnostics", func(t *testing.T) {
		e := inheritedPort()
		e.Content = `{"secret":"template-marker"}`
		e.Variables[0].Default = json.RawMessage(`"default-marker"`)
		p := ownContent(`{"x":"content-marker","x":0}`)
		p.Values = map[string]json.RawMessage{"port": json.RawMessage(`"value-marker"`)}
		defer checkContentInputs(t, &e, p)()
		got, issues := configuration.SelectElementContent(e, elementLocation, inheritedRouterLocation, p)
		issue := requireSelectionIssue(t, got, issues, "duplicate_json_key")
		path := "/x"
		want := configuration.InheritanceIssue{Issue: configuration.Issue{Code: "duplicate_json_key", ElementID: elementUUID, Source: "content", Path: &path}, Location: inheritedRouterLocation}
		if !reflect.DeepEqual(issue, want) {
			t.Fatalf("unsafe or inaccurate diagnostic: %+v", issue)
		}
		p.Content[6] = '!'
		if !reflect.DeepEqual(issue, want) {
			t.Fatal("diagnostic aliases input")
		}
		p.Content[6] = 'c'
	})
}

func transition(t *testing.T, current *configuration.ContentOverride, action configuration.ContentAction) *configuration.ContentOverride {
	t.Helper()
	e := inheritedPort()
	defer checkContentInputs(t, &e, current)()
	got, issues := configuration.TransitionContentOverride(elementUUID, elementLocation, inheritedRouterLocation, current, action)
	if len(issues) != 0 {
		t.Fatalf("transition failed: %+v", issues)
	}
	if action.Kind != configuration.ResetContentOverride && got == nil {
		t.Fatal("transition returned no candidate")
	}
	if got != nil && got.Source != inheritedSource() {
		t.Fatalf("wrong source: %+v", got.Source)
	}
	return got
}
func TestTransitionReplace(t *testing.T) {
	current := &configuration.ContentOverride{Source: inheritedSource(), Mode: configuration.ContentModeVariables, Values: map[string]json.RawMessage{"port": json.RawMessage(`"8443"`), "invalid": json.RawMessage(`broken`), "nil": nil, "empty": {}}}
	for _, raw := range []string{`{"port":443}`, `{"extension":null}`} {
		candidate := transition(t, current, configuration.ContentAction{Kind: configuration.ActivateReplacement, Content: json.RawMessage(raw)})
		if candidate.Mode != configuration.ContentModeReplacement || !reflect.DeepEqual(candidate.Values, current.Values) {
			t.Fatalf("lost state: %+v", candidate)
		}
		got, issues := configuration.SelectElementContent(inheritedPort(), elementLocation, inheritedRouterLocation, candidate)
		assertSelected(t, got, issues, configuration.ContentModeReplacement, raw)
		current = candidate
	}
}

func TestTransitionReturn(t *testing.T) {
	for _, tc := range []struct {
		name       string
		change     func(*configuration.Element)
		code, want string
	}{
		{"same", func(e *configuration.Element) {}, "", `{"port":"8443"}`},
		{"type", func(e *configuration.Element) {
			e.Variables[0].Type = "number"
			e.Variables[0].Default = json.RawMessage(`443`)
		}, "type_mismatch", ""},
		{"removed", func(e *configuration.Element) { e.Content = `{}`; e.Variables = nil }, "unknown_variable", ""},
		{"required", func(e *configuration.Element) {
			e.Variables = append(e.Variables, configuration.VariableDefinition{Name: "clientId", Type: "string"})
		}, "missing_value", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := inheritedPort()
			e.Variables[0].Type = "string"
			e.Variables[0].Default = json.RawMessage(`"443"`)
			current := &configuration.ContentOverride{Source: inheritedSource(), Mode: configuration.ContentModeVariables, Values: map[string]json.RawMessage{"port": json.RawMessage(`"8443"`)}}
			own := transition(t, current, configuration.ContentAction{Kind: configuration.ActivateReplacement, Content: json.RawMessage(`null`)})
			tc.change(&e)
			selected, issues := configuration.SelectElementContent(e, elementLocation, inheritedRouterLocation, own)
			assertSelected(t, selected, issues, configuration.ContentModeReplacement, `null`)
			candidate := transition(t, own, configuration.ContentAction{Kind: configuration.ReturnToVariables})
			if candidate.Mode != configuration.ContentModeVariables || candidate.Content != nil || !reflect.DeepEqual(candidate.Values, current.Values) {
				t.Fatalf("bad return: %+v", candidate)
			}
			check := checkContentInputs(t, &e, candidate)
			selected, issues = configuration.SelectElementContent(e, elementLocation, inheritedRouterLocation, candidate)
			if tc.code == "" {
				assertSelected(t, selected, issues, configuration.ContentModeVariables, tc.want)
			} else {
				issue := requireSelectionIssue(t, selected, issues, tc.code)
				if issue.Location != inheritedRouterLocation || issue.Source != "value" {
					t.Fatalf("wrong error owner: %+v", issue)
				}
			}
			check()
		})
	}
	candidate := transition(t, nil, configuration.ContentAction{Kind: configuration.ReturnToVariables})
	got, issues := configuration.SelectElementContent(inheritedPort(), elementLocation, inheritedRouterLocation, candidate)
	assertSelected(t, got, issues, configuration.ContentModeVariables, `{"port":443}`)
}

func TestTransitionReset(t *testing.T) {
	for _, mode := range []configuration.ContentMode{configuration.ContentModeVariables, configuration.ContentModeReplacement} {
		for _, required := range []bool{false, true} {
			t.Run(string(mode)+fmt.Sprint(required), func(t *testing.T) {
				e := inheritedPort()
				e.Variables[0].Default = json.RawMessage(`8443`)
				if required {
					e.Variables[0].Default = nil
				}
				current := ownContent(`{"port":443}`)
				current.Mode = mode
				current.Values = map[string]json.RawMessage{"port": json.RawMessage(`9443`)}
				candidate := transition(t, current, configuration.ContentAction{Kind: configuration.ResetContentOverride})
				if candidate != nil {
					t.Fatalf("reset retained state: %+v", candidate)
				}
				if again := transition(t, candidate, configuration.ContentAction{Kind: configuration.ResetContentOverride}); again != nil {
					t.Fatal("repeated reset retained state")
				}
				got, issues := configuration.SelectElementContent(e, elementLocation, inheritedRouterLocation, candidate)
				if required {
					requireSelectionIssue(t, got, issues, "missing_value")
				} else {
					assertSelected(t, got, issues, configuration.ContentModeVariables, `{"port":8443}`)
				}
				replacement := transition(t, candidate, configuration.ContentAction{Kind: configuration.ActivateReplacement, Content: json.RawMessage(`{"new":true}`)})
				restored := transition(t, replacement, configuration.ContentAction{Kind: configuration.ReturnToVariables})
				if len(restored.Values) != 0 || restored.Content != nil {
					t.Fatalf("reset resurrected data: %+v", restored)
				}
			})
		}
	}
}

func TestTransitionInvalid(t *testing.T) {
	for _, action := range []configuration.ContentActionKind{configuration.ActivateReplacement, configuration.ReturnToVariables, configuration.ResetContentOverride} {
		for _, tc := range contentIdentityCases() {
			t.Run(string(action)+"/"+tc.name, func(t *testing.T) {
				e := inheritedPort()
				tl, rl := elementLocation, inheritedRouterLocation
				p := ownContent(`broken`)
				tc.change(&e, &tl, &rl, p)
				defer checkContentInputs(t, &e, p)()
				got, issues := configuration.TransitionContentOverride(e.ID, tl, rl, p, configuration.ContentAction{Kind: action, Content: json.RawMessage(`broken`)})
				if got != nil || len(issues) != 1 || issues[0].Code != tc.code {
					t.Fatalf("got %+v, %+v; want %s", got, issues, tc.code)
				}
				id := elementUUID
				if tc.noID {
					id = ""
				}
				want := configuration.InheritanceIssue{Issue: configuration.Issue{Code: tc.code, Source: "record", ElementID: id}, Location: tc.location}
				if !reflect.DeepEqual(issues[0], want) {
					t.Fatalf("wrong identity error: %+v", issues[0])
				}
			})
		}
	}
	for _, tc := range replacementJSONCases() {
		t.Run("json/"+tc.name, func(t *testing.T) {
			for _, current := range []*configuration.ContentOverride{nil, ownContent(`null`), {Source: inheritedSource(), Mode: configuration.ContentModeVariables}} {
				e := inheritedPort()
				check := checkContentInputs(t, &e, current)
				got, issues := configuration.TransitionContentOverride(elementUUID, elementLocation, inheritedRouterLocation, current, configuration.ContentAction{Kind: configuration.ActivateReplacement, Content: tc.raw})
				if got != nil || len(issues) != 1 {
					t.Fatalf("accepted invalid content: %+v, %+v", got, issues)
				}
				checkContentJSONIssue(t, issues[0], tc)
				check()
			}
		})
	}
	for _, kind := range []configuration.ContentActionKind{"", "unknown"} {
		got, issues := configuration.TransitionContentOverride(elementUUID, elementLocation, inheritedRouterLocation, nil, configuration.ContentAction{Kind: kind, Content: json.RawMessage(`null`)})
		if got != nil || len(issues) != 1 || issues[0].Code != "invalid_element_record" || issues[0].Location != inheritedRouterLocation {
			t.Fatalf("accepted unknown action: %+v, %+v", got, issues)
		}
	}
	for _, kind := range []configuration.ContentActionKind{configuration.ReturnToVariables, configuration.ResetContentOverride} {
		p := ownContent(`broken`)
		p.Values = map[string]json.RawMessage{"invalid": json.RawMessage(`broken`)}
		transition(t, p, configuration.ContentAction{Kind: kind, Content: json.RawMessage(`broken`)})
	}
	candidate := transition(t, nil, configuration.ContentAction{Kind: configuration.ActivateReplacement, Content: json.RawMessage(`null`)})
	got, issues := configuration.SelectElementContent(inheritedPort(), elementLocation, inheritedRouterLocation, candidate)
	assertSelected(t, got, issues, configuration.ContentModeReplacement, `null`)
}

func TestTransitionSafety(t *testing.T) {
	for _, kind := range []configuration.ContentActionKind{configuration.ActivateReplacement, configuration.ReturnToVariables, configuration.ResetContentOverride} {
		for _, direction := range []string{"input", "output"} {
			t.Run(string(kind)+"/"+direction, func(t *testing.T) {
				current := ownContent(`{"old":true}`)
				current.Values = map[string]json.RawMessage{"x": json.RawMessage(`[9007199254740993,null]`), "nil": nil, "empty": {}}
				action := configuration.ContentAction{Kind: kind, Content: json.RawMessage(`{"new":true}`)}
				candidate := transition(t, current, action)
				// Discarding this candidate leaves the original state untouched (checked by transition).
				if candidate == nil {
					return
				}
				if !reflect.DeepEqual(candidate.Values, current.Values) {
					t.Fatal("values changed or were normalized")
				}
				if direction == "input" {
					current.Values["x"][0] = '!'
					current.Values["added"] = json.RawMessage(`false`)
					action.Content[0] = '!'
					if string(candidate.Values["x"]) != `[9007199254740993,null]` || candidate.Values["added"] != nil {
						t.Fatal("candidate aliases prior values")
					}
					if kind == configuration.ActivateReplacement && string(candidate.Content) != `{"new":true}` {
						t.Fatal("candidate aliases action content")
					}
				} else {
					candidate.Values["x"][0] = '!'
					candidate.Values["added"] = json.RawMessage(`true`)
					if candidate.Content != nil {
						candidate.Content[0] = '!'
					}
					if string(current.Values["x"]) != `[9007199254740993,null]` || current.Values["added"] != nil {
						t.Fatal("prior state aliases candidate values")
					}
					if string(action.Content) != `{"new":true}` || string(current.Content) != `{"old":true}` {
						t.Fatal("candidate mutated source content")
					}
				}
			})
		}
	}
}
