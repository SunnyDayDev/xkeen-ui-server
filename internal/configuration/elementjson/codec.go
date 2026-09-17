// Package elementjson encodes internal element records, not Xray or HTTP payloads.
package elementjson

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/jsonvalue"
)

type record struct {
	ID        string       `json:"id"`
	Content   string       `json:"content"`
	Variables []definition `json:"variables"`
}

type definition struct {
	Name    string          `json:"name"`
	Type    string          `json:"type"`
	Default json.RawMessage `json:"default,omitempty"`
}

// Encode prepares declarations and validates editing state before serialization.
// It does not check uniqueness without a complete owner set or write any files.
func Encode(input configuration.Element, location configuration.ElementLocation, originalID *string) ([]byte, []configuration.ElementIssue) {
	prepared, issues := configuration.PrepareElement(input, location, originalID)
	if len(issues) != 0 {
		return nil, issues
	}
	r := record{ID: prepared.ID, Content: prepared.Content, Variables: make([]definition, len(prepared.Variables))}
	for i, v := range prepared.Variables {
		r.Variables[i] = definition{v.Name, v.Type, v.Default}
	}
	// All raw defaults have already passed JSON validation.
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, recordIssue(location, "", nil, prepared.ID)
	}
	return raw, nil
}

// Decode restores the recorded declarations without discovering or repairing them.
func Decode(raw []byte, location configuration.ElementLocation) (*configuration.Element, []configuration.ElementIssue) {
	tree, failure := jsonvalue.Parse(raw)
	if failure != nil {
		issue := configuration.ElementIssue{Code: failure.Code, Location: location, Source: "record", Path: failure.Path, Offset: failure.Offset}
		// A duplicate within a default can be located relative to that default.
		if issue.Path != nil {
			parts := strings.SplitN(*issue.Path, "/", 5)
			if len(parts) >= 4 && parts[1] == "variables" && parts[3] == "default" {
				if index, err := strconv.Atoi(parts[2]); err == nil {
					path := ""
					if len(parts) == 5 {
						path = "/" + parts[4]
					}
					issue.Source, issue.DefinitionIndex, issue.Path = "default", &index, &path
				}
			}
		}
		return nil, []configuration.ElementIssue{issue}
	}
	object, ok := tree.(map[string]any)
	if !ok {
		return nil, recordIssue(location, "", nil, "")
	}
	// Missing, non-string and otherwise invalid IDs all go through the ID rule.
	id, _ := object["id"].(string)
	if !onlyFields(object, "id", "content", "variables") {
		return nil, recordIssue(location, "", nil, id)
	}
	content, ok := object["content"].(string)
	if !ok {
		return nil, recordIssue(location, "/content", nil, id)
	}
	variables, ok := object["variables"].([]any)
	if !ok {
		return nil, recordIssue(location, "/variables", nil, id)
	}
	e := configuration.Element{ID: id, Content: content, Variables: make([]configuration.VariableDefinition, len(variables))}
	for i, value := range variables {
		path := "/variables/" + strconv.Itoa(i)
		v, ok := value.(map[string]any)
		if !ok || !onlyFields(v, "name", "type", "default") {
			return nil, recordIssue(location, path, &i, id)
		}
		name, nameOK := v["name"].(string)
		kind, typeOK := v["type"].(string)
		if !nameOK || !typeOK {
			return nil, recordIssue(location, path, &i, id)
		}
		e.Variables[i] = configuration.VariableDefinition{Name: name, Type: kind}
		if def, present := v["default"]; present {
			// Parse uses json.Number; this cannot round numbers through float64.
			e.Variables[i].Default, _ = json.Marshal(def)
		}
	}
	return configuration.RestoreElement(e, location)
}

func onlyFields(object map[string]any, allowed ...string) bool {
	for key := range object {
		found := false
		for _, name := range allowed {
			if key == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func recordIssue(location configuration.ElementLocation, path string, index *int, id string) []configuration.ElementIssue {
	if !configuration.ValidElementID(id) {
		id = ""
	}
	source := "record"
	if index != nil {
		source = "definition"
	}
	return []configuration.ElementIssue{{Code: "invalid_element_record", Location: location, ElementID: id, Source: source, Path: &path, DefinitionIndex: index}}
}
