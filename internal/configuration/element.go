package configuration

import (
	"bytes"
	"encoding/json"
	"slices"
	"unicode/utf8"
)

// Element is stored editing state, not an assembled or validated Xray config.
type Element struct {
	ID        string
	Content   string
	Variables []VariableDefinition
}

type VariableDefinition struct {
	Name string
	Type string
	// Nil means absent; a non-nil buffer must hold one JSON value.
	Default json.RawMessage
}

type Owner struct{ Kind, ID string }
type ElementLocation struct {
	Owner    Owner
	Position string
}

// ElementIssue locates failures without retaining content or invalid IDs.
type ElementIssue struct {
	Code            string
	Location        ElementLocation
	OtherLocation   *ElementLocation
	ElementID       string
	Source          string
	DefinitionIndex *int
	Path            *string
	Offset          *int64
}

// RestoreElement validates stored editing state without discovering declarations.
func RestoreElement(input Element, location ElementLocation) (*Element, []ElementIssue) {
	if !validLocation(location) {
		return nil, invalidContext()
	}
	if !ValidElementID(input.ID) {
		return nil, []ElementIssue{{Code: "invalid_element_id", Location: location, Source: "record"}}
	}
	if issue := storedJSONIssue([]byte(input.Content), location, input.ID, "content", nil); issue != nil {
		return nil, []ElementIssue{*issue}
	}
	for i, v := range input.Variables {
		if !utf8.ValidString(v.Name) || !utf8.ValidString(v.Type) {
			return nil, []ElementIssue{{Code: "invalid_element_record", Location: location, ElementID: input.ID, Source: "definition", DefinitionIndex: &i}}
		}
		if v.Default != nil {
			if issue := storedJSONIssue(v.Default, location, input.ID, "default", &i); issue != nil {
				return nil, []ElementIssue{*issue}
			}
		}
	}
	input.Variables = slices.Clone(input.Variables)
	for i := range input.Variables {
		input.Variables[i].Default = bytes.Clone(input.Variables[i].Default)
	}
	return &input, nil
}

func storedJSONIssue(raw json.RawMessage, location ElementLocation, id, source string, index *int) *ElementIssue {
	_, issue := parseJSON(raw, Issue{})
	if issue == nil {
		return nil
	}
	return &ElementIssue{Code: issue.Code, Location: location, ElementID: id, Source: source, DefinitionIndex: index, Path: issue.Path, Offset: issue.Offset}
}

// ValidElementID reports canonical lower-case UUIDv4 identity.
func ValidElementID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
	}
	return id[14] == '4' && (id[19] == '8' || id[19] == '9' || id[19] == 'a' || id[19] == 'b')
}

// PrepareElement retains editing state and adds declarations for new whole references.
// originalID is nil for a new record and required when updating an existing one.
func PrepareElement(input Element, location ElementLocation, originalID *string) (*Element, []ElementIssue) {
	result, issues := RestoreElement(input, location)
	if len(issues) != 0 {
		return nil, issues
	}
	if originalID != nil && !ValidElementID(*originalID) {
		return nil, []ElementIssue{{Code: "invalid_element_id", Location: location, ElementID: input.ID, Source: "record"}}
	}
	if originalID != nil && *originalID != input.ID {
		return nil, []ElementIssue{{Code: "element_id_mismatch", Location: location, ElementID: input.ID, Source: "record"}}
	}
	result.Variables = append(result.Variables, discoverDefinitions(result.Content, result.Variables)...)
	return result, nil
}
