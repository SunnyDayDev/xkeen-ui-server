package configuration

import (
	"sort"
	"strings"
	"unicode"
)

func discoverDefinitions(content string, existing []VariableDefinition) []VariableDefinition {
	tree, _ := parseJSON([]byte(content), Issue{})
	names := map[string]bool{}
	collectNames(tree, names)
	for _, v := range existing {
		delete(names, v.Name)
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	definitions := make([]VariableDefinition, 0, len(ordered))
	for _, name := range ordered {
		definitions = append(definitions, VariableDefinition{Name: name, Type: "string", Default: []byte(`""`)})
	}
	return definitions
}

func collectNames(value any, names map[string]bool) {
	switch value := value.(type) {
	case string:
		if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
			name := value[2 : len(value)-1]
			if validVariableName(name) {
				names[name] = true
			}
		}
	case []any:
		for _, child := range value {
			collectNames(child, names)
		}
	case map[string]any:
		for _, child := range value {
			collectNames(child, names)
		}
	}
}

func validVariableName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '$' || r == '{' || r == '}' {
			return false
		}
	}
	return true
}
