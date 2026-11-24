package server

import (
	"net/http"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

func NewExternalRuleState(store ExternalRouterDraftStore, next ExternalOperation) ExternalOperation {
	return externalRouterElementOperation("/api/v1/routers/rules/state", store, externalRuleStateRepresentation, readExternalRuleState, next)
}
func externalRuleStateRepresentation(s routerdraft.Snapshot, id string) ([]byte, string, error) {
	state, err := routerdraft.ReadRuleState(s, id)
	if err != nil {
		return nil, "", err
	}
	return externalRouterDocument("external-rule-state", struct {
		externalRouterBasis
		routerdraft.RuleState
	}{externalRouterBasisFrom(s.Basis), state})
}
func readExternalRuleState(w http.ResponseWriter, r *http.Request) (externalRouterMutation, bool) {
	var input struct {
		Disabled *bool `json:"disabled"`
	}
	if !readExternalTemplateJSON(w, r, 4096, &input) {
		return nil, false
	}
	if input.Disabled == nil {
		externalError(w, 400, "invalid_request")
		return nil, false
	}
	return func(s routerdraft.Snapshot, id string) (routersettings.Draft, error) {
		return routerdraft.EditRuleState(s, id, *input.Disabled)
	}, true
}
