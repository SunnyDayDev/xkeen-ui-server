package configuration

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"
)

// PrepareValues checks definitions and selects values for one element. It does
// not validate template content or establish readiness for publication.
// Inputs remain unchanged; successful results own their buffers, and failures
// return no partial result. A present personal entry must contain valid JSON.
func PrepareValues(elementID string, definitions []VariableDefinition, personal map[string]json.RawMessage) (map[string]json.RawMessage, []Issue) {
	seen := make(map[string]bool, len(definitions))
	for i, def := range definitions {
		code, field := "", "name"
		switch {
		case !utf8.ValidString(def.Name) || !validVariableName(def.Name):
			code = "invalid_variable_name"
		case !validVariableType(def.Type):
			code, field = "invalid_variable_type", "type"
		case seen[def.Name]:
			code = "duplicate_variable"
		}
		if code != "" {
			path := "/" + strconv.Itoa(i) + "/" + field
			issue := Issue{Code: code, ElementID: elementID, Source: "definition", Path: &path}
			if utf8.ValidString(def.Name) {
				issue.VariableName = &def.Name
			}
			return nil, []Issue{issue}
		}
		seen[def.Name] = true
	}
	for name := range personal {
		if !seen[name] {
			issue := Issue{Code: "unknown_variable", ElementID: elementID, Source: "value"}
			if utf8.ValidString(name) {
				issue.VariableName = &name
			}
			return nil, []Issue{issue}
		}
	}
	result := make(map[string]json.RawMessage, len(definitions))
	for _, def := range definitions {
		loc := Issue{ElementID: elementID, VariableName: &def.Name, Source: "default"}
		defaultFilled := false
		if def.Default != nil {
			var issue *Issue
			defaultFilled, issue = validatePreparedValue(def.Default, def.Type, loc)
			if issue != nil {
				return nil, []Issue{*issue}
			}
		}
		raw, exists := personal[def.Name]
		loc.Source = "value"
		personalFilled := false
		if exists {
			var issue *Issue
			personalFilled, issue = validatePreparedValue(raw, def.Type, loc)
			if issue != nil {
				return nil, []Issue{*issue}
			}
		}
		if !personalFilled {
			if !defaultFilled {
				loc.Code = "missing_value"
				return nil, []Issue{loc}
			}
			raw = def.Default
		}
		result[def.Name] = bytes.Clone(raw)
	}
	return result, nil
}

func validVariableType(typ string) bool {
	switch typ {
	case "string", "number", "boolean", "object", "array":
		return true
	}
	return false
}

func validatePreparedValue(raw json.RawMessage, expectedType string, loc Issue) (bool, *Issue) {
	value, issue := parseJSON(raw, loc)
	if issue != nil {
		return false, issue
	}
	if path, found := nullPath(value, ""); found {
		loc.Code, loc.Path = "null_not_allowed", &path
		return false, &loc
	}
	if str, ok := value.(string); ok && strings.TrimSpace(str) == "" {
		return false, nil
	}
	actualType := ""
	switch value.(type) {
	case string:
		actualType = "string"
	case json.Number:
		actualType = "number"
	case bool:
		actualType = "boolean"
	case map[string]any:
		actualType = "object"
	case []any:
		actualType = "array"
	}
	if actualType != expectedType {
		path := ""
		loc.Code, loc.ExpectedType, loc.Path = "type_mismatch", expectedType, &path
		return false, &loc
	}
	return true, nil
}

func nullPath(value any, path string) (string, bool) {
	switch value := value.(type) {
	case nil:
		return path, true
	case map[string]any:
		for key, child := range value {
			if foundPath, found := nullPath(child, appendPath(path, key)); found {
				return foundPath, true
			}
		}
	case []any:
		for i, child := range value {
			if foundPath, found := nullPath(child, appendPath(path, strconv.Itoa(i))); found {
				return foundPath, true
			}
		}
	}
	return "", false
}
