package configuration

import (
	"bytes"
	"encoding/json"
)

// ContentMode chooses inherited variable assembly or literal personal content.
type ContentMode string

const (
	ContentModeVariables   ContentMode = "variables"
	ContentModeReplacement ContentMode = "replacement"
)

// ContentOverride is editable state for one shared source, not a storage format.
// Values remain inactive but retained in replacement mode.
type ContentOverride struct {
	Source  ElementSource
	Mode    ContentMode
	Values  map[string]json.RawMessage
	Content json.RawMessage
}

// SelectedContent owns its JSON buffer. Success does not validate Xray or a set.
type SelectedContent struct {
	Source  ElementSource
	Mode    ContentMode
	Content json.RawMessage
}

// SelectElementContent checks identity before selecting the active content branch.
// Nil personal state inherits defaults; literal JSON null is a successful result.
// Failure returns nil and issues. Inputs are never changed.
func SelectElementContent(element Element, templateLocation, routerLocation ElementLocation, personal *ContentOverride) (*SelectedContent, []InheritanceIssue) {
	var source *ElementSource
	if personal != nil {
		source = &personal.Source
	}
	if issues := validateInheritedSource(element.ID, templateLocation, routerLocation, source); len(issues) != 0 {
		return nil, issues
	}
	if personal != nil && personal.Mode != ContentModeVariables && personal.Mode != ContentModeReplacement {
		return nil, []InheritanceIssue{{Issue: Issue{Code: "invalid_element_record", Source: "record", ElementID: element.ID}, Location: routerLocation}}
	}
	if personal == nil || personal.Mode == ContentModeVariables {
		var values *VariableOverride
		if personal != nil {
			values = &VariableOverride{Source: personal.Source, Values: personal.Values}
		}
		inherited, issues := AssembleInheritedElement(element, templateLocation, routerLocation, values)
		if len(issues) != 0 {
			return nil, issues
		}
		return &SelectedContent{Source: inherited.Source, Mode: ContentModeVariables, Content: inherited.Content}, nil
	}
	if _, issue := parseJSON(personal.Content, Issue{ElementID: element.ID, Source: "content"}); issue != nil {
		return nil, []InheritanceIssue{{Issue: *issue, Location: routerLocation}}
	}
	return &SelectedContent{Source: ElementSource{TemplateID: templateLocation.Owner.ID, ElementID: element.ID}, Mode: ContentModeReplacement, Content: bytes.Clone(personal.Content)}, nil
}

// ContentActionKind describes an explicit change to editable content state.
type ContentActionKind string

const (
	ActivateReplacement  ContentActionKind = "replace"
	ReturnToVariables    ContentActionKind = "variables"
	ResetContentOverride ContentActionKind = "reset"
)

// ContentAction requires Content only for ActivateReplacement.
type ContentAction struct {
	Kind    ContentActionKind
	Content json.RawMessage
}

// TransitionContentOverride returns an independent candidate without assembling it.
// Reset succeeds with (nil, nil); failures return (nil, issues). The caller may
// discard the candidate to cancel an edit. The existing source is supplied by
// the caller; order, exclusions and persistence are outside this operation.
func TransitionContentOverride(elementID string, templateLocation, routerLocation ElementLocation, current *ContentOverride, action ContentAction) (*ContentOverride, []InheritanceIssue) {
	var source *ElementSource
	if current != nil {
		source = &current.Source
	}
	if issues := validateInheritedSource(elementID, templateLocation, routerLocation, source); len(issues) != 0 {
		return nil, issues
	}
	if (current != nil && current.Mode != ContentModeVariables && current.Mode != ContentModeReplacement) ||
		(action.Kind != ActivateReplacement && action.Kind != ReturnToVariables && action.Kind != ResetContentOverride) {
		return nil, []InheritanceIssue{{Issue: Issue{Code: "invalid_element_record", Source: "record", ElementID: elementID}, Location: routerLocation}}
	}
	if action.Kind == ActivateReplacement {
		if _, issue := parseJSON(action.Content, Issue{ElementID: elementID, Source: "content"}); issue != nil {
			return nil, []InheritanceIssue{{Issue: *issue, Location: routerLocation}}
		}
	}
	if action.Kind == ResetContentOverride {
		return nil, nil
	}
	var values map[string]json.RawMessage
	if current != nil && current.Values != nil {
		values = make(map[string]json.RawMessage, len(current.Values))
		for name, value := range current.Values {
			values[name] = bytes.Clone(value)
		}
	}
	mode := ContentModeVariables
	var content json.RawMessage
	if action.Kind == ActivateReplacement {
		mode, content = ContentModeReplacement, bytes.Clone(action.Content)
	}
	return &ContentOverride{Source: ElementSource{TemplateID: templateLocation.Owner.ID, ElementID: elementID}, Mode: mode, Values: values, Content: content}, nil
}
