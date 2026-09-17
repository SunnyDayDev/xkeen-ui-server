package configuration

import "encoding/json"

// AssembleElement resolves one element's variables, checking definitions,
// defaults, personal values and template syntax without changing any input.
// It returns independent JSON or issues with no partial result. The ID is only
// diagnostic context; success does not validate identity, Xray or other elements.
func AssembleElement(elementID string, template json.RawMessage, definitions []VariableDefinition, personal map[string]json.RawMessage) (json.RawMessage, []Issue) {
	values, issues := PrepareValues(elementID, definitions, personal)
	if len(issues) != 0 {
		return nil, issues
	}
	return SubstituteValues(elementID, template, values)
}
