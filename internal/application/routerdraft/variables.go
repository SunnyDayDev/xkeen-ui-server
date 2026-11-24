package routerdraft

import (
	"encoding/json"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type VariableSelection struct {
	ID          string                    `json:"id"`
	Section     string                    `json:"section"`
	Mode        configuration.ContentMode `json:"mode"`
	Definitions []VariableValue           `json:"definitions"`
}

type VariableValue struct {
	Name           string  `json:"name"`
	Type           string  `json:"type"`
	DefaultSource  *string `json:"defaultSource,omitempty"`
	PersonalSource *string `json:"personalSource,omitempty"`
	Required       bool    `json:"required"`
	Issue          string  `json:"issue,omitempty"`
}

func EditVariables(snapshot Snapshot, elementID string, changes []routersettings.VariableChange) (routersettings.Draft, error) {
	element, section, err := SharedElement(snapshot, elementID)
	if err != nil {
		return routersettings.Draft{}, err
	}
	source := configuration.ElementSource{TemplateID: snapshot.Basis.TemplateID, ElementID: elementID}
	return routersettings.EditVariableValues(snapshot.Editable, section, source, element.Variables, changes)
}

func InspectVariables(snapshot Snapshot, elementID string) (VariableSelection, error) {
	element, section, err := SharedElement(snapshot, elementID)
	if err != nil {
		return VariableSelection{}, err
	}
	source := configuration.ElementSource{TemplateID: snapshot.Basis.TemplateID, ElementID: elementID}
	override, err := routersettings.SharedOverride(snapshot.Editable, section, source)
	if err != nil {
		return VariableSelection{}, err
	}
	mode := configuration.ContentModeVariables
	var personal map[string]json.RawMessage
	if override != nil {
		mode = override.Mode
		personal = override.Values
	}
	result := VariableSelection{ID: elementID, Section: section, Mode: mode, Definitions: []VariableValue{}}
	if mode != configuration.ContentModeVariables {
		return result, nil
	}
	inspection := configuration.InspectVariableDefinitions(element.ID, element.Variables)
	for i, definition := range element.Variables {
		row := VariableValue{Name: definition.Name, Type: definition.Type, Required: inspection[i].Required}
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
		result.Definitions = append(result.Definitions, row)
	}
	return result, nil
}
