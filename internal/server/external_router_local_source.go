package server

import (
	"encoding/json"
	"net/http"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

func NewExternalLocalRuleSource(store ExternalRouterDraftStore, next ExternalOperation) ExternalOperation {
	return externalRouterElementOperation("/api/v1/routers/rules/local", store, externalLocalRuleRepresentation, readExternalLocalRulePatch, next)
}

func externalLocalRuleRepresentation(snapshot routerdraft.Snapshot, id string) ([]byte, string, error) {
	source, err := routerdraft.ReadLocalRule(snapshot, id)
	if err != nil {
		return nil, "", err
	}
	return externalRouterDocument("external-local-rule-source", struct {
		externalRouterBasis
		ElementID string `json:"elementId"`
		Source    string `json:"source"`
	}{externalRouterBasisFrom(snapshot.Basis), id, source})
}

func readExternalLocalRulePatch(w http.ResponseWriter, r *http.Request) (externalRouterMutation, bool) {
	var input struct {
		Source *string `json:"source"`
	}
	if !readExternalTemplateJSON(w, r, maxWebEditorBody, &input) {
		return nil, false
	}
	if input.Source == nil {
		externalError(w, 400, "invalid_request")
		return nil, false
	}
	return func(snapshot routerdraft.Snapshot, id string) (routersettings.Draft, error) {
		return routerdraft.EditLocalRule(snapshot, id, json.RawMessage(*input.Source))
	}, true
}
