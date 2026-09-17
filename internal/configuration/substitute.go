// Package configuration contains the rules for assembling configuration JSON.
package configuration

import (
	"encoding/json"
	"strconv"
)

// Issue locates a problem without retaining the input JSON or supplied data.
type Issue struct {
	ExpectedType string // Set only for type_mismatch.
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
// It validates the original template's nulls and reference/escaping syntax.
// Callers must first prepare names and selected values with PrepareValues;
// definitions, defaults and required values are not checked here. Use
// AssembleElement for the complete variable contract of one element. Neither
// operation validates Xray or establishes readiness for publication.
func SubstituteValues(elementID string, template json.RawMessage, values map[string]json.RawMessage) (json.RawMessage, []Issue) {
	tree, issue := parseJSON(template, Issue{ElementID: elementID, Source: "template"})
	if issue != nil {
		return nil, []Issue{*issue}
	}
	if path, found := nullPath(tree, ""); found {
		return nil, []Issue{{Code: "null_not_allowed", ElementID: elementID, Source: "template", Path: &path}}
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
	if s, ok := tree.(string); ok {
		literal, name, code := parseTemplateString(s)
		if code != "" {
			return nil, &Issue{Code: code, ElementID: elementID, Source: "template", VariableName: name, Path: &path}
		}
		if name == nil {
			return literal, nil
		}
		if value, found := values[*name]; found {
			return value, nil
		}
		return nil, &Issue{Code: "unknown_variable", ElementID: elementID, Source: "template", VariableName: name, Path: &path}
	}
	return tree, nil
}
