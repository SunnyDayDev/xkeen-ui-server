package configuration

import (
	"encoding/json"
	"testing"
)

func TestInspectVariableDefinitionsDoesNotRequirePersonalValues(t *testing.T) {
	definitions := []VariableDefinition{
		{Name: "missing", Type: "number"},
		{Name: "blank", Type: "number", Default: json.RawMessage(`" \u00a0 "`)},
		{Name: "false", Type: "boolean", Default: json.RawMessage(`false`)},
		{Name: "zero", Type: "number", Default: json.RawMessage(`0`)},
		{Name: "obj", Type: "object", Default: json.RawMessage(`{}`)},
		{Name: "list", Type: "array", Default: json.RawMessage(`[]`)},
		{Name: "wrong", Type: "number", Default: json.RawMessage(`"443"`)},
		{Name: "null", Type: "object", Default: json.RawMessage(`{"x":null}`)},
		{Name: "missing", Type: "number"},
		{Name: "broken name", Type: "number"},
	}
	inspected := InspectVariableDefinitions("example", definitions)
	if len(inspected) != len(definitions) {
		t.Fatalf("inspection length: %d", len(inspected))
	}
	for i := range inspected {
		wantIssue := map[int]string{6: "type_mismatch", 7: "null_not_allowed", 8: "duplicate_variable", 9: "invalid_variable_name"}[i]
		got := ""
		if inspected[i].Issue != nil {
			got = inspected[i].Issue.Code
		}
		if inspected[i].Required != (i < 2) || got != wantIssue {
			t.Fatalf("definition %d required=%v, issue=%s", i, inspected[i].Required, got)
		}
	}
	mentions := VariableReferences(`{"a":"$` + "{missing}" + `","$` + "{ignored}" + `":"$$` + "{literal}" + `"}`)
	if !mentions["missing"] || len(mentions) != 1 {
		t.Fatalf("wrong reference set: %v", mentions)
	}
}
