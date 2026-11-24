package routersettings

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestEditRouterVariablesPreservesExactSourcesAndOtherSettings(t *testing.T) {
	source := configuration.ElementSource{TemplateID: "template", ElementID: "550e8400-e29b-41d4-a716-446655440000"}
	original := configuration.ContentOverride{Source: source, Mode: configuration.ContentModeVariables, Values: map[string]json.RawMessage{
		"inherited": json.RawMessage("7"), "stale.name": json.RawMessage("5"),
	}}
	base := Draft{ID: "router", Name: "router", TemplateID: source.TemplateID, Groups: []Group{
		{Name: "outbounds", Overrides: []configuration.ContentOverride{original}},
		{Name: "inbounds", Exclusions: []configuration.ElementSource{{TemplateID: source.TemplateID, ElementID: "550e8400-e29b-41d4-a716-446655440001"}}},
	}}
	definitions := []configuration.VariableDefinition{{Name: "inherited", Type: "number"}, {Name: "options", Type: "object"}}
	raw := json.RawMessage("{\r\n \"n\":9007199254740993, \"enabled\":false\n}\r\n")
	candidate, err := EditVariableValues(base, "outbounds", source, definitions, []VariableChange{
		{Name: "inherited"}, {Name: "options", Value: raw},
	})
	if err != nil {
		t.Fatal(err)
	}
	saved := candidate.Groups[0].Overrides[0]
	if saved.Source != source || saved.Mode != configuration.ContentModeVariables || len(saved.Values) != 2 ||
		string(saved.Values["options"]) != string(raw) || string(saved.Values["stale.name"]) != "5" ||
		!reflect.DeepEqual(base.Groups[1], candidate.Groups[1]) || len(base.Groups[0].Overrides[0].Values) != 2 {
		t.Fatal("wrong personal change or unrelated data was overwritten")
	}
	raw[0] = ' '
	saved.Values["stale.name"][0] = '0'
	if string(base.Groups[0].Overrides[0].Values["stale.name"]) != "5" || saved.Values["options"][0] != '{' {
		t.Fatal("candidate did not own the changed record")
	}
	changes := []VariableChange{{Name: "options", Value: json.RawMessage("null")}}
	candidate, err = EditVariableValues(base, "outbounds", source, definitions, changes)
	if err != nil || string(candidate.Groups[0].Overrides[0].Values["options"]) != "null" {
		t.Fatal("storable null was converted or rejected")
	}
	for _, bad := range [][]VariableChange{
		{{Name: "misspelled"}},
		{{Name: "options"}, {Name: "options", Value: json.RawMessage("false")}},
	} {
		if _, err := EditVariableValues(base, "outbounds", source, definitions, bad); !errors.Is(err, ErrInvalidDraft) {
			t.Fatalf("bad field not rejected: %v", err)
		}
	}
	var invalid *VariableSourceError
	if _, err := EditVariableValues(base, "outbounds", source, definitions, []VariableChange{{Name: "options", Value: json.RawMessage(`{"a":1,"a":2}`)}}); !errors.As(err, &invalid) || invalid.Name != "options" {
		t.Fatalf("invalid JSON missing safe diagnosis: %v", err)
	}
	if _, err := EditVariableValues(base, "rules", source, definitions, changes); !errors.Is(err, ErrSharedScope) {
		t.Fatalf("scope mismatch not rejected: %v", err)
	}
	base.Groups[0].Overrides[0].Mode = configuration.ContentModeReplacement
	base.Groups[0].Overrides[0].Content = json.RawMessage(`{}`)
	if _, err := EditVariableValues(base, "outbounds", source, definitions, changes); !errors.Is(err, ErrVariableMode) {
		t.Fatalf("replacement was silently switched: %v", err)
	}
}
