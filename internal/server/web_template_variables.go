package server

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"net/http"
	"strconv"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func serveWebTemplateVariables(w http.ResponseWriter, r *http.Request, origin WebOrigin, editor webTemplateEditor, sessions *webadmin.Service) {
	if !allowWebEndpoint(w, r, origin, true, http.MethodGet, http.MethodPost) {
		return
	}
	id, elementID, ok := templateEditorQuery(r.URL.RawQuery, true)
	if !ok || elementID == "" {
		webResult(w, http.StatusBadRequest, "invalid_request", "")
		return
	}
	token := webSessionToken(r)
	var snapshot *templates.Snapshot
	var err error
	if r.Method == http.MethodGet {
		snapshot, err = editor.Read(r.Context(), token, id)
	} else {
		var proof string
		if values := r.Header.Values("X-CSRF-Token"); len(values) == 1 {
			proof = values[0]
		}
		if err := sessions.CheckMutation(r.Context(), token, proof); err != nil {
			webFailure(w, err)
			return
		}
		version, definitions, status := readWebVariablesEdit(w, r)
		if status != 0 {
			webEditorBodyFailure(w, status)
			return
		}
		snapshot, err = editor.SaveDefinitions(r.Context(), token, proof, id, elementID, version, definitions)
	}
	if err != nil {
		webTemplateEditorFailure(w, err)
		return
	}
	result, ok := webVariablesSnapshot(snapshot, elementID)
	if !ok {
		webResult(w, http.StatusNotFound, "element_not_found", "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func readWebVariablesEdit(w http.ResponseWriter, r *http.Request) (int64, []configuration.VariableDefinition, int) {
	raw, status := readWebEditorBody(w, r)
	if status != 0 {
		return 0, nil, status
	}

	var input struct {
		Version     string           `json:"version"`
		Definitions []*variableInput `json:"definitions"`
	}
	if jsonv2.Unmarshal(raw, &input, jsonv2.RejectUnknownMembers(true)) != nil || input.Definitions == nil {
		return 0, nil, http.StatusBadRequest
	}
	version, err := strconv.ParseInt(input.Version, 10, 64)
	if err != nil || version < 1 || strconv.FormatInt(version, 10) != input.Version {
		return 0, nil, http.StatusBadRequest
	}
	definitions, ok := decodeVariableInputs(input.Definitions)
	if !ok {
		return 0, nil, http.StatusBadRequest
	}
	return version, definitions, 0
}

type variableInput struct {
	Name          *string         `json:"name"`
	Type          *string         `json:"type"`
	DefaultSource json.RawMessage `json:"defaultSource"`
}

func decodeVariableInputs(items []*variableInput) ([]configuration.VariableDefinition, bool) {
	if items == nil {
		return nil, false
	}
	definitions := make([]configuration.VariableDefinition, len(items))
	for i, item := range items {
		if item == nil || item.Name == nil || item.Type == nil {
			return nil, false
		}
		definitions[i] = configuration.VariableDefinition{Name: *item.Name, Type: *item.Type}
		if item.DefaultSource != nil {
			var text *string
			if jsonv2.Unmarshal(item.DefaultSource, &text) != nil || text == nil {
				return nil, false
			}
			definitions[i].Default = json.RawMessage(*text)
		}
	}
	return definitions, true
}

type webVariableDefinition struct {
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	DefaultSource *string `json:"defaultSource,omitempty"`
	Required      bool    `json:"required"`
	Referenced    bool    `json:"referenced"`
	Issue         string  `json:"issue,omitempty"`
}

type webVariableSnapshot struct {
	ID          string                  `json:"id"`
	ElementID   string                  `json:"elementId"`
	Version     string                  `json:"version"`
	Definitions []webVariableDefinition `json:"definitions"`
}

func webVariablesSnapshot(snapshot *templates.Snapshot, id string) (webVariableSnapshot, bool) {
	for _, section := range snapshot.Sections {
		for _, member := range section.Elements {
			item := member.Element
			if item.ID == id {
				return describeWebVariableSnapshot(snapshot, item), true
			}
		}
	}
	return webVariableSnapshot{}, false
}

func describeWebVariableSnapshot(snapshot *templates.Snapshot, element configuration.Element) webVariableSnapshot {
	inspections := configuration.InspectVariableDefinitions(element.ID, element.Variables)
	references := configuration.VariableReferences(element.Content)
	result := webVariableSnapshot{ID: snapshot.ID, ElementID: element.ID, Version: strconv.FormatInt(snapshot.Version, 10),
		Definitions: make([]webVariableDefinition, len(element.Variables))}
	for i, def := range element.Variables {
		row := webVariableDefinition{Name: def.Name, Type: def.Type, Required: inspections[i].Required, Referenced: references[def.Name]}
		if def.Default != nil {
			text := string(def.Default)
			row.DefaultSource = &text
		}
		if issue := inspections[i].Issue; issue != nil {
			row.Issue = issue.Code
		}
		result.Definitions[i] = row
	}
	return result
}
