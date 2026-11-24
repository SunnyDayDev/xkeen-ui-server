package templates

import (
	"errors"
	"slices"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

var (
	ErrElementNotFound  = errors.New("template element not found")
	ErrTemplateNotEmpty = errors.New("template already has elements")
)

var firstElementSections = []string{"log", "api", "inbounds", "outbounds", "routing", "rules", "dns", "policy", "stats", "observatory", "fakedns"}

func FirstElementSections() []string {
	return slices.Clone(firstElementSections)
}

// EditElementContent builds the independent candidate shared by browser and
// external writers. Only the selected element undergoes A08 discovery.
func EditElementContent(base *Snapshot, elementID, source string) (configuration.TemplateDraft, error) {
	return editElement(base, elementID, func(item *configuration.Element) error {
		item.Content = source
		return nil
	})
}

// EditElementDefinitions preserves identity, content and the ordered definition names.
func EditElementDefinitions(base *Snapshot, elementID string, definitions []configuration.VariableDefinition) (configuration.TemplateDraft, error) {
	return editElement(base, elementID, func(item *configuration.Element) error {
		if len(item.Variables) != len(definitions) {
			return ErrInvalid
		}
		for i, definition := range definitions {
			if item.Variables[i].Name != definition.Name {
				return ErrInvalid
			}
		}
		item.Variables = definitions
		return nil
	})
}

func editElement(base *Snapshot, elementID string, change func(*configuration.Element) error) (configuration.TemplateDraft, error) {
	if base == nil || !configuration.ValidElementID(elementID) {
		return configuration.TemplateDraft{}, ErrInvalid
	}
	candidate, err := RestoreSnapshot(base.ID, base)
	if err != nil {
		return configuration.TemplateDraft{}, err
	}
	for i, section := range candidate.Sections {
		for j, item := range section.Elements {
			if item.Element.ID != elementID {
				continue
			}
			if err := change(&item.Element); err != nil {
				return configuration.TemplateDraft{}, err
			}
			element, issues := configuration.PrepareElement(item.Element, candidate.ElementLocation(i, j), &elementID)
			if len(issues) != 0 {
				return configuration.TemplateDraft{}, &InvalidDraftError{Issues: issues}
			}
			candidate.Sections[i].Elements[j].Element = *element
			return candidate.TemplateDraft, nil
		}
	}
	return configuration.TemplateDraft{}, ErrElementNotFound
}

// CreateFirstElement adds one item to a complete, verified-empty draft.
func CreateFirstElement(base *Snapshot, sectionName, newID, source string) (configuration.TemplateDraft, error) {
	if base == nil || !configuration.ValidElementID(newID) || !slices.Contains(firstElementSections, sectionName) {
		return configuration.TemplateDraft{}, ErrInvalid
	}
	candidate, err := RestoreSnapshot(base.ID, base)
	if err != nil {
		return configuration.TemplateDraft{}, err
	}
	target := len(candidate.Sections)
	for i, section := range candidate.Sections {
		if len(section.Elements) != 0 {
			return configuration.TemplateDraft{}, ErrTemplateNotEmpty
		}
		if section.Name == sectionName {
			target = i
		}
	}
	if target == len(candidate.Sections) {
		candidate.Sections = append(candidate.Sections, configuration.TemplateSection{Name: sectionName})
	}
	element, issues := configuration.PrepareElement(configuration.Element{ID: newID, Content: source}, candidate.ElementLocation(target, 0), nil)
	if len(issues) != 0 {
		return configuration.TemplateDraft{}, &InvalidDraftError{Issues: issues}
	}
	candidate.Sections[target].Elements = []configuration.TemplateElement{{Element: *element}}
	return candidate.TemplateDraft, nil
}
