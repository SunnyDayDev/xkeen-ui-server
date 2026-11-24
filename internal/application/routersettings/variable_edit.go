package routersettings

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/jsonvalue"
)

var (
	ErrSharedScope  = errors.New("shared variable scope changed")
	ErrVariableMode = errors.New("shared element uses literal replacement")
)

type VariableChange struct {
	Name  string
	Value json.RawMessage // Nil explicitly inherits the definition's default.
}

type VariableSourceError struct {
	Name  string
	Issue jsonvalue.Issue
}

func (e *VariableSourceError) Error() string { return "invalid personal JSON" }

func SharedOverride(base Draft, section string, source configuration.ElementSource) (*configuration.ContentOverride, error) {
	if base.TemplateID != source.TemplateID || section == "" || !configuration.ValidElementID(source.ElementID) {
		return nil, ErrSharedScope
	}
	for i := range base.Groups {
		group := &base.Groups[i]
		for j := range group.Overrides {
			override := &group.Overrides[j]
			if override.Source != source {
				continue
			}
			if group.Name != section {
				return nil, ErrSharedScope
			}
			return override, nil
		}
	}
	return nil, nil
}

// EditVariableValues copies the affected group and changes only the named values
// of one shared element. Other draft settings and legacy values are retained.
func EditVariableValues(base Draft, section string, source configuration.ElementSource, definitions []configuration.VariableDefinition, changes []VariableChange) (Draft, error) {
	previous, err := SharedOverride(base, section, source)
	if err != nil {
		return Draft{}, err
	}
	if previous != nil && previous.Mode != configuration.ContentModeVariables {
		return Draft{}, ErrVariableMode
	}
	if len(changes) == 0 {
		return Draft{}, ErrInvalidDraft
	}
	allowed := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		allowed[definition.Name] = true
	}

	next := configuration.ContentOverride{Source: source, Mode: configuration.ContentModeVariables}
	if previous != nil {
		next.Values = make(map[string]json.RawMessage, len(previous.Values))
		for name, value := range previous.Values {
			next.Values[name] = bytes.Clone(value)
		}
	}
	for _, change := range changes {
		if !allowed[change.Name] {
			return Draft{}, ErrInvalidDraft
		}
		delete(allowed, change.Name)
		if change.Value == nil {
			delete(next.Values, change.Name)
			continue
		}
		if _, issue := jsonvalue.Parse(change.Value); issue != nil {
			return Draft{}, &VariableSourceError{Name: change.Name, Issue: *issue}
		}
		if next.Values == nil {
			next.Values = make(map[string]json.RawMessage)
		}
		next.Values[change.Name] = bytes.Clone(change.Value)
	}
	if previous == nil && len(next.Values) == 0 {
		return base, nil
	}
	return withSharedOverride(base, section, source, &next), nil
}

func withSharedOverride(base Draft, section string, source configuration.ElementSource, next *configuration.ContentOverride) Draft {
	base.Groups = slices.Clone(base.Groups)
	for i, group := range base.Groups {
		if group.Name != section {
			continue
		}
		overrides := slices.Clone(group.Overrides)
		for j, override := range overrides {
			if override.Source != source {
				continue
			}
			if next == nil {
				overrides = slices.Delete(overrides, j, j+1)
			} else {
				overrides[j] = *next
			}
			base.Groups[i].Overrides = overrides
			return base
		}
		if next != nil {
			base.Groups[i].Overrides = append(overrides, *next)
		}
		return base
	}
	if next != nil {
		base.Groups = append(base.Groups, Group{Name: section, Overrides: []configuration.ContentOverride{*next}})
	}
	return base
}
