package templates

import (
	"errors"
	"fmt"
	"slices"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

var (
	ErrElementNotFound  = errors.New("template element not found")
	ErrTemplateNotEmpty = errors.New("template already has elements")
	ErrElementExists    = fmt.Errorf("%w: template element id already exists", ErrInvalid)
	ErrSectionOccupied  = errors.New("template section already has an element")
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
	return insertElement(candidate, target, sectionName, newID, source)
}

func AppendElementSections(base *Snapshot) []string {
	sections := FirstElementSections()
	for _, section := range base.Sections {
		if len(section.Elements) != 0 && !collectionSection(section.Name) {
			sections = slices.DeleteFunc(sections, func(name string) bool { return name == section.Name })
		}
	}
	return sections
}

func collectionSection(name string) bool {
	switch name {
	case "inbounds", "outbounds", "rules", "fakedns":
		return true
	}
	return false
}

// AddElement chooses the empty or populated template operation from the snapshot.
func AddElement(base *Snapshot, sectionName, newID, source string) (configuration.TemplateDraft, error) {
	if base != nil {
		for _, section := range base.Sections {
			if len(section.Elements) != 0 {
				return AppendElement(base, sectionName, newID, source)
			}
		}
	}
	return CreateFirstElement(base, sectionName, newID, source)
}

// AppendElement adds one item while retaining all existing records in order.
func AppendElement(base *Snapshot, sectionName, newID, source string) (configuration.TemplateDraft, error) {
	if base == nil || !configuration.ValidElementID(newID) || !slices.Contains(firstElementSections, sectionName) {
		return configuration.TemplateDraft{}, ErrInvalid
	}
	candidate, err := RestoreSnapshot(base.ID, base)
	if err != nil {
		return configuration.TemplateDraft{}, err
	}
	target := len(candidate.Sections)
	populated := false
	for i, section := range candidate.Sections {
		for _, item := range section.Elements {
			populated = true
			if item.Element.ID == newID {
				return configuration.TemplateDraft{}, ErrElementExists
			}
		}
		if section.Name == sectionName {
			if len(section.Elements) != 0 && !collectionSection(sectionName) {
				return configuration.TemplateDraft{}, ErrSectionOccupied
			}
			target = i
		}
	}
	if !populated {
		return configuration.TemplateDraft{}, ErrInvalid
	}
	return insertElement(candidate, target, sectionName, newID, source)
}

func insertElement(candidate *Snapshot, target int, sectionName, newID, source string) (configuration.TemplateDraft, error) {
	if target == len(candidate.Sections) {
		candidate.Sections = append(candidate.Sections, configuration.TemplateSection{Name: sectionName})
	}
	section := &candidate.Sections[target]
	element, issues := configuration.PrepareElement(configuration.Element{ID: newID, Content: source}, candidate.ElementLocation(target, len(section.Elements)), nil)
	if len(issues) != 0 {
		return configuration.TemplateDraft{}, &InvalidDraftError{Issues: issues}
	}
	section.Elements = append(section.Elements, configuration.TemplateElement{Element: *element})
	return candidate.TemplateDraft, nil
}
