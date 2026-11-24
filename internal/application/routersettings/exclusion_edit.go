package routersettings

import (
	"encoding/json"
	"maps"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type ExclusionOverride struct {
	Source      configuration.ElementSource
	Values      map[string]string
	Replacement *string
}

type ExclusionEdit struct {
	Action string
	Values map[string]string
	Text   *string
}

func (override *ExclusionOverride) valid(draft Draft) bool {
	if override == nil {
		return true
	}
	refs := []configuration.OverrideRef{{Source: override.Source, Position: "/exclusion"}}
	if len(configuration.ValidateOverrideSources(configuration.Owner{Kind: "template", ID: draft.TemplateID}, configuration.Owner{Kind: "router", ID: draft.ID}, refs)) != 0 {
		return false
	}
	for name, value := range override.Values {
		if !validID(name) || !utf8.ValidString(value) {
			return false
		}
	}
	return override.Replacement == nil || utf8.ValidString(*override.Replacement)
}

func EditExclusion(base Draft, source configuration.ElementSource, edit ExclusionEdit) (Draft, error) {
	next := ExclusionOverride{Source: source}
	if current := base.Exclusion; current != nil {
		if current.Source != source {
			return Draft{}, ErrInvalidDraft
		}
		next.Values = maps.Clone(current.Values)
		next.Replacement = current.Replacement
	}
	if !next.valid(base) || (edit.Action == "replace") != (edit.Text != nil) || (edit.Action == "values") != (edit.Values != nil) {
		return Draft{}, ErrInvalidDraft
	}
	switch edit.Action {
	case "replace":
		raw := *edit.Text
		next.Replacement = &raw
	case "return":
		if next.Replacement == nil {
			return Draft{}, ErrInvalidDraft
		}
		next.Replacement = nil
	case "values":
		if next.Replacement != nil {
			return Draft{}, ErrInvalidDraft
		}
		next.Values = maps.Clone(edit.Values)
	case "reset":
		if base.Exclusion == nil {
			return Draft{}, ErrInvalidDraft
		}
		base.Exclusion = nil
		return base, nil
	default:
		return Draft{}, ErrInvalidDraft
	}
	if !next.valid(base) {
		return Draft{}, ErrInvalidDraft
	}
	base.Exclusion = &next
	return base, nil
}

func (d Draft) AssembleExclusions(source *configuration.ExclusionSource) (*configuration.ExclusionFile, *configuration.ExclusionIssue) {
	if d.Exclusion == nil {
		return configuration.AssembleXKeenExclusions(source, nil, nil)
	}
	if source == nil || d.Exclusion.Source != (configuration.ElementSource{TemplateID: d.TemplateID, ElementID: source.ElementID}) {
		return nil, &configuration.ExclusionIssue{Code: "source_mismatch"}
	}
	values := make(map[string]json.RawMessage, len(d.Exclusion.Values))
	for name, value := range d.Exclusion.Values {
		values[name], _ = json.Marshal(value)
	}
	return configuration.AssembleXKeenExclusions(source, values, d.Exclusion.Replacement)
}
