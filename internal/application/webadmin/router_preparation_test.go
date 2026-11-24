package webadmin

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

const (
	firstPreparationElement  = "550e8400-e29b-41d4-a716-446655440000"
	secondPreparationElement = "550e8400-e29b-41d4-a716-446655440001"
	preparationRouter        = "550e8400-e29b-41d4-a716-446655440002"
)

func preparationFixture() *templates.Snapshot {
	return &templates.Snapshot{Version: 3, TemplateDraft: configuration.TemplateDraft{ID: "template / 1", Sections: []configuration.TemplateSection{{
		Name: "outbounds", Elements: []configuration.TemplateElement{
			{Element: configuration.Element{ID: firstPreparationElement, Content: `{"tag":"east"}`, Variables: []configuration.VariableDefinition{{Name: "value", Type: "number"}, {Name: "flag", Type: "boolean", Default: json.RawMessage("false")}}}},
			{Element: configuration.Element{ID: secondPreparationElement, Content: `{"tag":"west"}`, Variables: []configuration.VariableDefinition{{Name: "value", Type: "string", Default: json.RawMessage(`""`)}}}},
		},
	}}}}
}

func TestPreparationProjectsRequiredAndExactDefault(t *testing.T) {
	draft := preparationFixture()
	fields, err := describePreparation(draft)
	if err != nil || len(fields) != 2 {
		t.Fatalf("projected %#v: %v", fields, err)
	}
	if fields[0].ElementID != firstPreparationElement || fields[0].Section != "outbounds" || !fields[0].Variables[0].Required || fields[0].Variables[1].Required ||
		fields[0].Variables[1].DefaultSource != "false" || fields[1].ElementID != secondPreparationElement || !fields[1].Variables[0].Required {
		t.Fatalf("incorrect variable projection: %#v", fields)
	}
	draft.Sections[0].Elements[0].Element.Variables[1].Default = json.RawMessage("null")
	if _, err := describePreparation(draft); !errors.Is(err, ErrInvalidPreparation) {
		t.Fatalf("invalid inherited default: %v", err)
	}
}

func TestPreparationValuesAreScopedAndLossless(t *testing.T) {
	template := preparationFixture()
	valid := []PersonalInput{
		{ElementID: firstPreparationElement, Name: "value", Source: "9007199254740993"},
		{ElementID: secondPreparationElement, Name: "value", Source: `"node.example"`},
	}
	draft, err := prepareRouterSettings(template, preparationRouter, "Филиал Восток", valid)
	if err != nil || draft.ID != preparationRouter || draft.Name != "Филиал Восток" || draft.TemplateID != template.ID || len(draft.Groups) != 1 {
		t.Fatalf("prepared settings: %#v %v", draft, err)
	}
	a, b := draft.Groups[0].Overrides[0], draft.Groups[0].Overrides[1]
	if a.Source.ElementID != firstPreparationElement || string(a.Values["value"]) != "9007199254740993" || a.Values["flag"] != nil ||
		b.Source.ElementID != secondPreparationElement || string(b.Values["value"]) != `"node.example"` {
		t.Fatalf("inexact or mis-scoped values: %#v %#v", a, b)
	}
	for _, row := range []struct {
		name, code string
		values     []PersonalInput
	}{
		{"missing", "missing_value", valid[:1]},
		{"wrong-type", "type_mismatch", []PersonalInput{{ElementID: firstPreparationElement, Name: "value", Source: `"text"`}, valid[1]}},
		{"other uuid", "unknown_element", append(append([]PersonalInput{}, valid...), PersonalInput{ElementID: preparationRouter, Name: "value", Source: "7"})},
		{"unknown name", "unknown_variable", append(append([]PersonalInput{}, valid...), PersonalInput{ElementID: firstPreparationElement, Name: "extra", Source: "7"})},
		{"dup", "duplicate_value", append(append([]PersonalInput{}, valid...), valid[0])},
	} {
		t.Run(row.name, func(t *testing.T) {
			_, err := prepareRouterSettings(template, preparationRouter, "Филиал Восток", row.values)
			var issue *PreparationInputError
			if !errors.As(err, &issue) || issue.Code != row.code {
				t.Fatalf("value error: %v", err)
			}
		})
	}
}
