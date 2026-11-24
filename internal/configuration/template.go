package configuration

import (
	"fmt"
	"unicode/utf8"
)

// TemplateDraft is the editable configuration of a shared template.
type TemplateDraft struct {
	ID       string
	Sections []TemplateSection
}

type TemplateSection struct {
	Name     string
	Elements []TemplateElement
}

type TemplateElement struct {
	Element  Element
	Disabled bool
}

// ValidateTemplateDraft checks the metadata of the full template before any
// individual element is prepared or restored.
func ValidateTemplateDraft(draft TemplateDraft) []ElementIssue {
	owner := Owner{Kind: "template", ID: draft.ID}
	if !validOwner(owner) {
		return invalidContext()
	}
	names := make(map[string]struct{}, len(draft.Sections))
	var refs []ElementRef
	for i, section := range draft.Sections {
		_, duplicate := names[section.Name]
		if section.Name == "" || !utf8.ValidString(section.Name) || duplicate {
			return []ElementIssue{{Code: "invalid_template_section", Location: ElementLocation{
				Owner: owner, Position: fmt.Sprintf("/sections/%d", i),
			}, Source: "record"}}
		}
		names[section.Name] = struct{}{}
		for j, item := range section.Elements {
			refs = append(refs, ElementRef{ID: item.Element.ID, Position: draft.ElementLocation(i, j).Position})
		}
	}
	return ValidateTemplateIDs(owner, refs)
}

// ElementLocation gives the address of an element within the complete draft.
func (draft TemplateDraft) ElementLocation(section, element int) ElementLocation {
	return ElementLocation{
		Owner:    Owner{Kind: "template", ID: draft.ID},
		Position: fmt.Sprintf("/sections/%d/elements/%d", section, element),
	}
}
