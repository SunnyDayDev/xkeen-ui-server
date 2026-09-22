package configuration

import (
	"encoding/json"
	"unicode/utf8"
)

// ElementSource identifies a shared element by its template and UUID.
type ElementSource struct {
	TemplateID string
	ElementID  string
}

// VariableOverride holds personal values for exactly one shared source.
type VariableOverride struct {
	Source ElementSource
	Values map[string]json.RawMessage
}

// InheritedContent is assembled content, not a validated router configuration.
type InheritedContent struct {
	Source  ElementSource
	Content json.RawMessage
}

// InheritanceIssue adds owner and element position to safe assembly diagnostics.
type InheritanceIssue struct {
	Issue
	Location ElementLocation
}

// AssembleInheritedElement checks the shared source, then assembles its current
// content using B02. Inputs are unchanged; success owns its JSON buffer, and
// failures return no content. This does not validate a collection, order or Xray.
func AssembleInheritedElement(element Element, templateLocation, routerLocation ElementLocation, personal *VariableOverride) (*InheritedContent, []InheritanceIssue) {
	var source *ElementSource
	var values map[string]json.RawMessage
	if personal != nil {
		source = &personal.Source
		values = personal.Values
	}
	if issues := validateInheritedSource(element.ID, templateLocation, routerLocation, source); len(issues) != 0 {
		return nil, issues
	}
	content, issues := AssembleElement(element.ID, json.RawMessage(element.Content), element.Variables, values)
	if len(issues) != 0 {
		result := make([]InheritanceIssue, len(issues))
		for i, issue := range issues {
			location := templateLocation
			if issue.Source == "value" {
				location = routerLocation
			}
			result[i] = InheritanceIssue{Issue: issue, Location: location}
		}
		return nil, result
	}
	return &InheritedContent{Source: ElementSource{TemplateID: templateLocation.Owner.ID, ElementID: element.ID}, Content: content}, nil
}

func validateInheritedSource(elementID string, templateLocation, routerLocation ElementLocation, personal *ElementSource) []InheritanceIssue {
	id := elementID
	if !ValidElementID(id) {
		id = ""
	}
	fail := func(code string, location ElementLocation) []InheritanceIssue {
		return []InheritanceIssue{{Issue: Issue{Code: code, ElementID: id, Source: "record"}, Location: location}}
	}
	if templateLocation.Owner.Kind != "template" || routerLocation.Owner.Kind != "router" || !validLocation(templateLocation) || !validLocation(routerLocation) {
		return fail("invalid_element_record", ElementLocation{})
	}
	if id == "" {
		return fail("invalid_element_id", templateLocation)
	}
	if personal != nil {
		if personal.TemplateID == "" || !utf8.ValidString(personal.TemplateID) {
			return fail("invalid_element_record", routerLocation)
		}
		if !ValidElementID(personal.ElementID) {
			return fail("invalid_element_id", routerLocation)
		}
		if personal.ElementID != elementID {
			return fail("element_id_mismatch", routerLocation)
		}
		if personal.TemplateID != templateLocation.Owner.ID {
			return fail("source_mismatch", routerLocation)
		}
	}
	return nil
}
