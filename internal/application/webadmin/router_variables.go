package webadmin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/jsonvalue"
)

type RouterVariableEditor struct {
	store RouterDraftStore
	now   func() time.Time
}

func NewRouterVariableEditor(store RouterDraftStore, now func() time.Time) RouterVariableEditor {
	return RouterVariableEditor{store: store, now: now}
}

type RouterVariables struct {
	Basis     RouterDraftBasis
	Name      string
	Sections  []RouterVariableSection
	Selection *RouterVariableSelection
}

type RouterVariableSection struct {
	Name    string                 `json:"name"`
	Members []RouterVariableMember `json:"members"`
}

type RouterVariableMember struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type RouterVariableSelection struct {
	ID          string                    `json:"id"`
	Section     string                    `json:"section"`
	Mode        configuration.ContentMode `json:"mode"`
	Definitions []RouterVariableValue     `json:"definitions"`
}

type RouterVariableValue struct {
	Name           string  `json:"name"`
	Type           string  `json:"type"`
	DefaultSource  *string `json:"defaultSource,omitempty"`
	PersonalSource *string `json:"personalSource,omitempty"`
	Required       bool    `json:"required"`
	Issue          string  `json:"issue,omitempty"`
}

func (e RouterVariableEditor) Read(ctx context.Context, token, routerID, elementID string) (RouterVariables, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return RouterVariables{}, err
	}
	snapshot, err := e.store.Read(ctx, digest, e.now, routerID)
	if err != nil {
		return RouterVariables{}, err
	}
	return readRouterVariables(snapshot, elementID)
}

func (e RouterVariableEditor) Save(ctx context.Context, token, csrf string, basis RouterDraftBasis, elementID string, changes []routersettings.VariableChange) (RouterVariables, error) {
	digest, _, err := SessionProof(token, csrf, true)
	if err != nil {
		return RouterVariables{}, err
	}
	snapshot, err := e.store.Read(ctx, digest, e.now, basis.RouterID)
	if err != nil {
		return RouterVariables{}, err
	}
	if snapshot.Basis != basis {
		return RouterVariables{}, ErrRouterDraftConflict
	}
	if snapshot.Editable.TemplateID != basis.TemplateID {
		return RouterVariables{}, ErrRouterDraftForeign
	}
	element, section, err := sharedVariableElement(snapshot, elementID)
	if err != nil {
		return RouterVariables{}, err
	}
	source := configuration.ElementSource{TemplateID: basis.TemplateID, ElementID: elementID}
	snapshot.Editable, err = routersettings.EditVariableValues(snapshot.Editable, section, source, element.Variables, changes)
	if err != nil {
		return RouterVariables{}, err
	}
	result, err := readRouterVariables(snapshot, elementID)
	if err != nil {
		return RouterVariables{}, err
	}
	generation, err := e.store.Write(ctx, digest, e.now, basis, snapshot.Editable)
	if err != nil {
		return RouterVariables{}, err
	}
	result.Basis.DraftGeneration = generation
	return result, nil
}

func sharedVariableElement(snapshot RouterDraftSnapshot, elementID string) (configuration.Element, string, error) {
	if snapshot.Accepted != nil && configuration.ValidElementID(elementID) {
		for _, section := range snapshot.Accepted.Sections {
			for _, item := range section.Elements {
				if item.Element.ID == elementID {
					return item.Element, section.Name, nil
				}
			}
		}
	}
	return configuration.Element{}, "", ErrRouterDraftMissing
}

func readRouterVariables(snapshot RouterDraftSnapshot, elementID string) (RouterVariables, error) {
	if snapshot.Editable.TemplateID != snapshot.Basis.TemplateID {
		return RouterVariables{}, ErrRouterDraftForeign
	}
	result := RouterVariables{Basis: snapshot.Basis, Name: snapshot.Name, Sections: make([]RouterVariableSection, 0, len(snapshot.Accepted.Sections))}
	for _, section := range snapshot.Accepted.Sections {
		if len(section.Elements) == 0 {
			continue
		}
		group := RouterVariableSection{Name: section.Name, Members: make([]RouterVariableMember, 0, len(section.Elements))}
		for _, item := range section.Elements {
			element := item.Element
			label := element.ID
			if tree, issue := jsonvalue.Parse([]byte(element.Content)); issue == nil {
				if object, ok := tree.(map[string]any); ok {
					if tag, ok := object["tag"].(string); ok && tag != "" {
						label = tag
					}
				}
			}
			group.Members = append(group.Members, RouterVariableMember{ID: element.ID, Label: label})
		}
		result.Sections = append(result.Sections, group)
	}
	if elementID == "" {
		return result, nil
	}
	element, section, err := sharedVariableElement(snapshot, elementID)
	if err != nil {
		return RouterVariables{}, err
	}
	source := configuration.ElementSource{TemplateID: snapshot.Basis.TemplateID, ElementID: elementID}
	override, err := routersettings.VariableOverride(snapshot.Editable, section, source)
	if err != nil {
		return RouterVariables{}, err
	}
	mode := configuration.ContentModeVariables
	var personal map[string]json.RawMessage
	if override != nil {
		mode = override.Mode
		personal = override.Values
	}
	result.Selection = &RouterVariableSelection{ID: elementID, Section: section, Mode: mode, Definitions: []RouterVariableValue{}}
	if mode != configuration.ContentModeVariables {
		return result, nil
	}
	inspection := configuration.InspectVariableDefinitions(element.ID, element.Variables)
	for i, definition := range element.Variables {
		row := RouterVariableValue{Name: definition.Name, Type: definition.Type, Required: inspection[i].Required}
		if definition.Default != nil {
			value := string(definition.Default)
			row.DefaultSource = &value
		}
		var local map[string]json.RawMessage
		if value, ok := personal[definition.Name]; ok {
			raw := string(value)
			row.PersonalSource = &raw
			local = map[string]json.RawMessage{definition.Name: value}
		}
		if issue := inspection[i].Issue; issue != nil {
			row.Issue = issue.Code
		} else if _, issues := configuration.PrepareValues(element.ID, []configuration.VariableDefinition{definition}, local); len(issues) != 0 {
			row.Issue = issues[0].Code
		}
		result.Selection.Definitions = append(result.Selection.Definitions, row)
	}
	return result, nil
}
