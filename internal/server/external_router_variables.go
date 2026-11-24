package server

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"net/http"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

const externalVariablesPath = "/api/v1/routers/variables"

func NewExternalRouterVariables(store ExternalRouterDraftStore, next ExternalOperation) ExternalOperation {
	return externalRouterElementOperation(externalVariablesPath, store, externalVariableRepresentation, readExternalVariablePatch, next)
}

func readExternalVariablePatch(w http.ResponseWriter, r *http.Request) (externalRouterMutation, bool) {
	changes, ok := externalVariableChanges(w, r)
	if !ok {
		return nil, false
	}
	return func(snapshot routerdraft.Snapshot, id string) (routersettings.Draft, error) {
		return routerdraft.EditVariables(snapshot, id, changes)
	}, true
}

func externalVariableChanges(w http.ResponseWriter, r *http.Request) ([]routersettings.VariableChange, bool) {
	var input struct {
		Values []struct {
			Name   string          `json:"name"`
			Source json.RawMessage `json:"source"`
		} `json:"values"`
	}
	if !readExternalTemplateJSON(w, r, maxWebEditorBody, &input) {
		return nil, false
	}
	if len(input.Values) == 0 {
		externalError(w, http.StatusBadRequest, "invalid_request")
		return nil, false
	}
	changes := make([]routersettings.VariableChange, len(input.Values))
	for i, value := range input.Values {
		changes[i].Name = value.Name
		if value.Source != nil {
			var source *string
			if err := jsonv2.Unmarshal(value.Source, &source); err != nil || source == nil {
				externalError(w, http.StatusBadRequest, "invalid_request")
				return nil, false
			}
			changes[i].Value = json.RawMessage(*source)
		}
	}
	return changes, true
}

func externalVariableRepresentation(snapshot routerdraft.Snapshot, id string) ([]byte, string, error) {
	selection, err := routerdraft.InspectVariables(snapshot, id)
	if err != nil {
		return nil, "", err
	}
	return externalRouterDocument("external-router-variables", struct {
		externalRouterBasis
		routerdraft.VariableSelection
	}{externalRouterBasisFrom(snapshot.Basis), selection})
}
