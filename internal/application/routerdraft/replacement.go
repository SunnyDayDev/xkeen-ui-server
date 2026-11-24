package routerdraft

import (
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type ReplacementSelection struct {
	ElementID   string                    `json:"elementId"`
	Section     string                    `json:"section"`
	Mode        configuration.ContentMode `json:"mode"`
	HasOverride bool                      `json:"hasOverride"`
	Source      *string                   `json:"source,omitempty"`
}

func EditReplacement(snapshot Snapshot, id string, action configuration.ContentAction) (routersettings.Draft, error) {
	_, section, err := SharedElement(snapshot, id)
	if err != nil {
		return routersettings.Draft{}, err
	}
	source := configuration.ElementSource{TemplateID: snapshot.Basis.TemplateID, ElementID: id}
	return routersettings.EditReplacement(snapshot.Editable, section, source, action)
}

func InspectReplacement(snapshot Snapshot, id string) (ReplacementSelection, error) {
	_, section, err := SharedElement(snapshot, id)
	if err != nil {
		return ReplacementSelection{}, err
	}
	source := configuration.ElementSource{TemplateID: snapshot.Basis.TemplateID, ElementID: id}
	override, err := routersettings.SharedOverride(snapshot.Editable, section, source)
	if err != nil {
		return ReplacementSelection{}, err
	}
	result := ReplacementSelection{ElementID: id, Section: section, Mode: configuration.ContentModeVariables, HasOverride: override != nil}
	if override != nil {
		result.Mode = override.Mode
		if override.Mode == configuration.ContentModeReplacement {
			content := string(override.Content)
			result.Source = &content
		}
	}
	return result, nil
}
