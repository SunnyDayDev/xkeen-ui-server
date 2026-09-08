// Package configuration contains the rules for assembling configuration JSON.
package configuration

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Issue locates a problem without retaining the input JSON or supplied data.
type Issue struct {
	Code         string
	ElementID    string
	Source       string
	VariableName *string
	Path         *string
	Offset       *int64
}

// SubstituteValues replaces whole ${name} values using a prepared set for one
// element. Values retain their JSON types; all inputs are read-only. On failure
// the result is nil and issues locate the problem without retaining input data.
//
// Callers must prepare valid names and selected values first. This internal step
// is not a complete configuration validator: definitions, defaults, required
// values, null checks and the full reference/escaping grammar belong to the
// later preparation and validation stages. Do not publish its result directly.
func SubstituteValues(elementID string, template json.RawMessage, values map[string]json.RawMessage) (json.RawMessage, []Issue) {
	tree, issue := parseJSON(template, Issue{ElementID: elementID, Source: "template"})
	if issue != nil {
		return nil, []Issue{*issue}
	}
	prepared := make(map[string]any, len(values))
	for name, raw := range values {
		value, issue := parseJSON(raw, Issue{ElementID: elementID, Source: "value", VariableName: &name})
		if issue != nil {
			return nil, []Issue{*issue}
		}
		prepared[name] = value
	}
	tree, issue = substitute(tree, elementID, prepared, "")
	if issue != nil {
		return nil, []Issue{*issue}
	}
	// The tree contains only parsed JSON types and valid json.Number values.
	// Replacements are separate parsed trees, so they cannot introduce cycles.
	result, _ := json.Marshal(tree)
	return result, nil
}

func substitute(tree any, elementID string, values map[string]any, path string) (any, *Issue) {
	if array, ok := tree.([]any); ok {
		for i, value := range array {
			replaced, issue := substitute(value, elementID, values, appendPath(path, strconv.Itoa(i)))
			if issue != nil {
				return nil, issue
			}
			array[i] = replaced
		}
		return array, nil
	}
	if object, ok := tree.(map[string]any); ok {
		for key, value := range object {
			replaced, issue := substitute(value, elementID, values, appendPath(path, key))
			if issue != nil {
				return nil, issue
			}
			object[key] = replaced
		}
		return object, nil
	}
	if s, ok := tree.(string); ok && strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") {
		name := s[2 : len(s)-1]
		if value, found := values[name]; found {
			return value, nil
		}
		return nil, &Issue{Code: "unknown_variable", ElementID: elementID, Source: "template", VariableName: &name, Path: &path}
	}
	return tree, nil
}
