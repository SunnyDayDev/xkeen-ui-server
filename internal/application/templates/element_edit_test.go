package templates

import (
	"errors"
	"reflect"
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
