package configuration_test

import (
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

const elementUUID = "550e8400-e29b-41d4-a716-446655440000"
const otherUUID = "550e8400-e29b-41d4-a716-446655440001"

var elementLocation = configuration.ElementLocation{Owner: configuration.Owner{Kind: "template", ID: "template-a"}, Position: "/outbounds/0"}

func TestRestoreElement(t *testing.T) {
	in := configuration.Element{ID: elementUUID, Content: `{"tag":"primary-proxy"}`}
	got, issues := configuration.RestoreElement(in, elementLocation)
	if got == nil || len(issues) != 0 || !reflect.DeepEqual(*got, in) {
		t.Fatalf("restore = %+v, %+v; want unchanged element", got, issues)
	}
}

func requireElementIssue(t *testing.T, got *configuration.Element, issues []configuration.ElementIssue, code string) configuration.ElementIssue {
	t.Helper()
	if got != nil || len(issues) == 0 || issues[0].Code != code {
		t.Fatalf("got %+v, %+v; want nil and %s", got, issues, code)
	}
	return issues[0]
}

func TestInvalidElementID(t *testing.T) {
	for _, id := range []string{"", "outbound-demo", "00000000-0000-0000-0000-000000000000", "550E8400-E29B-41D4-A716-446655440000", " " + elementUUID, "{" + elementUUID + "}", "urn:uuid:" + elementUUID, "550e8400-e29b-11d4-a716-446655440000", "550e8400-e29b-41d4-7716-446655440000", "550e8400-e29b-41d4-g716-446655440000"} {
		t.Run(id, func(t *testing.T) {
			got, issues := configuration.RestoreElement(configuration.Element{ID: id, Content: `{}`}, elementLocation)
			issue := requireElementIssue(t, got, issues, "invalid_element_id")
			if issue.ElementID != "" || issue.Location != elementLocation {
				t.Fatalf("unsafe or incomplete issue: %+v", issue)
			}
		})
	}
}

func TestUpdateElementIdentity(t *testing.T) {
	original := elementUUID
	in := configuration.Element{ID: elementUUID, Content: `{"tag":"backup-proxy"}`}
	got, issues := configuration.PrepareElement(in, elementLocation, &original)
	if got == nil || len(issues) != 0 || !reflect.DeepEqual(*got, in) {
		t.Fatalf("update = %+v, %+v", got, issues)
	}
	in.ID = otherUUID
	got, issues = configuration.PrepareElement(in, elementLocation, &original)
	requireElementIssue(t, got, issues, "element_id_mismatch")
	original = "invalid-original"
	got, issues = configuration.PrepareElement(in, elementLocation, &original)
	requireElementIssue(t, got, issues, "invalid_element_id")
}

func TestStoredJSONIntegrity(t *testing.T) {
	for _, tc := range []struct{ name, raw, code string }{
		{"empty", "", "invalid_json"}, {"unfinished", `{"a":`, "invalid_json"},
		{"second_root", `true false`, "invalid_json"}, {"trailing", `{} garbage`, "invalid_json"},
		{"utf8", string([]byte{34, 255, 34}), "invalid_json"},
		{"duplicate", `{"nested":{"a":1,"\u0061":2}}`, "duplicate_json_key"},
	} {
		for _, source := range []string{"content", "default"} {
			t.Run(tc.name+"/"+source, func(t *testing.T) {
				in := configuration.Element{ID: elementUUID, Content: tc.raw}
				if source == "default" {
					in.Content = `{}`
					in.Variables = []configuration.VariableDefinition{{Name: "options", Type: "object", Default: []byte(tc.raw)}}
				}
				for _, prepare := range []bool{false, true} {
					var got *configuration.Element
					var issues []configuration.ElementIssue
					if prepare {
						got, issues = configuration.PrepareElement(in, elementLocation, nil)
					} else {
						got, issues = configuration.RestoreElement(in, elementLocation)
					}
					issue := requireElementIssue(t, got, issues, tc.code)
					if issue.Source != source || issue.ElementID != elementUUID || issue.Location != elementLocation {
						t.Fatalf("wrong context: %+v", issue)
					}
					if source == "default" && (issue.DefinitionIndex == nil || *issue.DefinitionIndex != 0) {
						t.Fatalf("missing definition index: %+v", issue)
					}
					if tc.code == "duplicate_json_key" && (issue.Path == nil || *issue.Path != "/nested/a") {
						t.Fatalf("wrong path: %+v", issue)
					}
					if tc.code == "invalid_json" && issue.Offset == nil {
						t.Fatal("missing syntax offset")
					}
				}
			})
		}
	}
}

func TestElementInputIsolation(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		t.Run(map[bool]string{false: "restore", true: "prepare"}[prepare], func(t *testing.T) {
			backing := []configuration.VariableDefinition{{Name: "kept", Type: "object", Default: []byte(`{"x":1}`)}, {Name: "sentinel", Type: "string"}}
			input := configuration.Element{ID: elementUUID, Content: `"${new}"`, Variables: backing[:1]}
			var got *configuration.Element
			var issues []configuration.ElementIssue
			if prepare {
				got, issues = configuration.PrepareElement(input, elementLocation, nil)
			} else {
				got, issues = configuration.RestoreElement(input, elementLocation)
			}
			if got == nil || len(issues) != 0 {
				t.Fatal(issues)
			}
			if backing[1].Name != "sentinel" {
				t.Error("append changed input backing capacity")
			}
			got.Variables[0].Name = "changed"
			got.Variables[0].Default[5] = '2'
			if input.Variables[0].Name != "kept" || string(input.Variables[0].Default) != `{"x":1}` {
				t.Fatal("result aliases input")
			}
		})
	}
}

func TestElementStructuralStringsAndContext(t *testing.T) {
	invalid := string([]byte{255})
	for _, tc := range []struct {
		name     string
		location configuration.ElementLocation
		vars     []configuration.VariableDefinition
	}{
		{"context", configuration.ElementLocation{}, nil},
		{"position", configuration.ElementLocation{Owner: elementLocation.Owner, Position: "/bad~"}, nil},
		{"name_utf8", elementLocation, []configuration.VariableDefinition{{Name: invalid, Type: "string"}}},
		{"type_utf8", elementLocation, []configuration.VariableDefinition{{Name: "a", Type: invalid}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, issues := configuration.RestoreElement(configuration.Element{ID: elementUUID, Content: `{}`, Variables: tc.vars}, tc.location)
			requireElementIssue(t, got, issues, "invalid_element_record")
		})
	}
}
