package templates

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

const editID = "550e8400-e29b-41d4-a716-446655440000"
const siblingID = "550e8400-e29b-41d4-a716-446655440001"

func TestEditContentPreservesTheTargetIdentityAndLegacySibling(t *testing.T) {
	base := &Snapshot{Version: 1, TemplateDraft: configuration.TemplateDraft{ID: "template/example", Sections: []configuration.TemplateSection{
		{Name: "inbounds", Elements: []configuration.TemplateElement{{Element: configuration.Element{ID: siblingID, Content: `"${old.sibling}"`}}}},
		{Name: "outbounds", Elements: []configuration.TemplateElement{{Disabled: true, Element: configuration.Element{ID: editID, Content: `{"tag":"original"}`, Variables: []configuration.VariableDefinition{{Name: "prior", Type: "number", Default: []byte("0")}}}}}},
	}}}
	raw := "{\r\n  \"x\":\"$" + "{new.current}\",\n  \"n\":9007199254740993\r\n}\n"
	changed, err := EditElementContent(base, editID, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(changed.Sections[0], base.Sections[0]) {
		t.Fatal("sibling changed")
	}
	item := changed.Sections[1].Elements[0]
	if item.Element.Content != raw || item.Element.ID != editID || !item.Disabled || len(item.Element.Variables) != 2 || item.Element.Variables[1].Name != "new.current" {
		t.Fatalf("unexpected edited element metadata: %#v", item.Element.Variables)
	}
	if base.Sections[1].Elements[0].Element.Content == raw {
		t.Fatal("input modified")
	}
	_, err = EditElementContent(base, editID, `{"x":1,"x":2}`)
	var invalid *InvalidDraftError
	if !errors.As(err, &invalid) || len(invalid.Issues) == 0 {
		t.Fatalf("invalid source diagnosis: %v", err)
	}
	if _, err := EditElementContent(base, "550e8400-e29b-41d4-a716-446655440019", `{}`); !errors.Is(err, ErrElementNotFound) {
		t.Fatalf("wrong scope: %v", err)
	}
}

func TestFirstElementUsesTheEmptyGroupAndNeverAppendsToNonemptyDraft(t *testing.T) {
	base := &Snapshot{Version: 1, TemplateDraft: configuration.TemplateDraft{ID: "template-example", Sections: []configuration.TemplateSection{{Name: "log"}, {Name: "outbounds"}}}}
	if _, err := CreateFirstElement(base, "exclude_ips", editID, `{}`); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported section: %v", err)
	}
	result, err := CreateFirstElement(base, "outbounds", editID, `null`)
	if err != nil || len(result.Sections) != 2 || len(result.Sections[0].Elements) != 0 || len(result.Sections[1].Elements) != 1 {
		t.Fatalf("first element: %v", err)
	}
	if _, err := CreateFirstElement(&Snapshot{Version: 2, TemplateDraft: result}, "log", siblingID, `{}`); !errors.Is(err, ErrTemplateNotEmpty) {
		t.Fatalf("repeated first element: %v", err)
	}
}

func TestEditDefinitionsRetainsSourceAndRequiresTheSameOrderedNames(t *testing.T) {
	original := "{\r\n  \"x\":\"$" + "{host}\"\n}\r\n"
	base := &Snapshot{Version: 1, TemplateDraft: configuration.TemplateDraft{ID: "template/example", Sections: []configuration.TemplateSection{
		{Name: "outbounds", Elements: []configuration.TemplateElement{{Disabled: true, Element: configuration.Element{ID: editID, Content: original, Variables: []configuration.VariableDefinition{{Name: "host", Type: "string", Default: []byte(`""`)}, {Name: "previous", Type: "object"}}}}}},
	}}}
	defs := []configuration.VariableDefinition{{Name: "host", Type: "number", Default: []byte("9007199254740993")}, {Name: "previous", Type: "object", Default: []byte("null")}}
	changed, err := EditElementDefinitions(base, editID, defs)
	if err != nil {
		t.Fatal(err)
	}
	item := changed.Sections[0].Elements[0]
	if item.Element.Content != original || item.Element.ID != editID || !item.Disabled || !reflect.DeepEqual(item.Element.Variables, defs) {
		t.Fatalf("definition edit changed unrelated element state: %#v", item.Element.Variables)
	}
	defs[0].Default[0] = '0'
	if string(item.Element.Variables[0].Default) != "9007199254740993" {
		t.Fatal("candidate aliased the supplied default")
	}
	defs[0].Name = "foreign"
	if _, err := EditElementDefinitions(base, editID, defs); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreign definition accepted: %v", err)
	}
	defs[0] = configuration.VariableDefinition{Name: "host", Type: "number", Default: []byte("{")}
	var invalid *InvalidDraftError
	if _, err := EditElementDefinitions(base, editID, defs); !errors.As(err, &invalid) || invalid.Issues[0].DefinitionIndex == nil || *invalid.Issues[0].DefinitionIndex != 0 {
		t.Fatalf("invalid syntax was accepted or not scoped: %v", err)
	}
}

func TestEditDefinitionsAppliesA08ToTheSelectedLegacyElement(t *testing.T) {
	older := []configuration.VariableDefinition{{Name: "retained", Type: "string"}}
	raw := `{"late":"$` + "{newly.discovered}" + `","old":"$` + "{retained}" + `"}`
	base := &Snapshot{Version: 1, TemplateDraft: configuration.TemplateDraft{ID: "older-template", Sections: []configuration.TemplateSection{{
		Name: "outbounds", Elements: []configuration.TemplateElement{{Element: configuration.Element{
			ID: editID, Content: raw, Variables: older,
		}}},
	}}}}
	changed, err := EditElementDefinitions(base, editID, []configuration.VariableDefinition{{Name: "retained", Type: "boolean"}})
	if err != nil {
		t.Fatal(err)
	}
	item := changed.Sections[0].Elements[0].Element
	want := []configuration.VariableDefinition{
		{Name: "retained", Type: "boolean"},
		{Name: "newly.discovered", Type: "string", Default: []byte(`""`)},
	}
	if item.Content != raw || !reflect.DeepEqual(item.Variables, want) ||
		!reflect.DeepEqual(base.Sections[0].Elements[0].Element.Variables, older) {
		t.Fatal("selected element did not retain ordered names and add only its missing declaration")
	}
}

func TestAppendElementPreservesExistingRulesAndExactSource(t *testing.T) {
	const fresh = "d21e8a4a-9eb3-4f69-8c31-5dd4d77fdce2"
	original := configuration.TemplateElement{Disabled: true, Element: configuration.Element{
		ID: editID, Content: `"${old.rule}"`,
	}}
	base := &Snapshot{Version: 3, TemplateDraft: configuration.TemplateDraft{ID: "example-template",
		Sections: []configuration.TemplateSection{
			{Name: "rules", Elements: []configuration.TemplateElement{original}},
			{Name: "log", Elements: []configuration.TemplateElement{{Element: configuration.Element{ID: siblingID, Content: `{}`}}}},
			{Name: "outbounds"},
		},
	}}
	raw := "{\r\n  \"x\":\"$" + "{new.rule}\",\n  \"n\":9007199254740993\r\n}\n"
	if names := AppendElementSections(base); !slices.Contains(names, "rules") || !slices.Contains(names, "outbounds") || slices.Contains(names, "log") {
		t.Fatalf("wrong append choices: %v", names)
	}
	draft, err := AppendElement(base, "rules", fresh, raw)
	if err != nil {
		t.Fatal(err)
	}
	items := draft.Sections[0].Elements
	if len(items) != 2 || !reflect.DeepEqual(items[0], original) || items[1].Element.ID != fresh || items[1].Element.Content != raw ||
		len(items[1].Element.Variables) != 1 || items[1].Element.Variables[0].Name != "new.rule" {
		t.Fatal("append lost identity, order, exact source or discovery")
	}
	if len(base.Sections[0].Elements) != 1 || !reflect.DeepEqual(draft.Sections[1], base.Sections[1]) ||
		draft.Sections[2].Name != "outbounds" || len(draft.Sections[2].Elements) != 0 {
		t.Fatal("append mutated the input or another group")
	}
	if _, err := AppendElement(base, "rules", siblingID, `{}`); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-section UUID collision: %v", err)
	}
	if _, err := AppendElement(base, "log", fresh, `{}`); !errors.Is(err, ErrSectionOccupied) {
		t.Fatalf("occupied singleton: %v", err)
	}
	if draft, err := AppendElement(base, "outbounds", fresh, `null`); err != nil || len(draft.Sections) != 3 || len(draft.Sections[2].Elements) != 1 {
		t.Fatalf("existing empty group: %v", err)
	}
	if _, err := AppendElement(base, "exclude_ips", fresh, `{}`); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported group: %v", err)
	}
	var invalid *InvalidDraftError
	if _, err := AppendElement(base, "rules", fresh, `{"x":0,"x":1}`); !errors.As(err, &invalid) {
		t.Fatalf("invalid source: %v", err)
	}
}
