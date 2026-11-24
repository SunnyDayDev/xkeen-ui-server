package server

import (
	"encoding/json"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"net/http"
	"strconv"
)

func NewWebRouterExclusionsHandler(origin WebOrigin, editor webadmin.RouterExclusionEditor, admission webPreparationAdmission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/routers/exclusions" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebEndpoint(w, r, origin, true, http.MethodGet, http.MethodPost) {
			return
		}
		id, element, ok := routerElementQuery(r.URL.RawQuery)
		if !ok || element != "" {
			webResult(w, 400, "invalid_request", "")
			return
		}
		token := webSessionToken(r)
		var state webadmin.RouterExclusions
		var err error
		if r.Method == http.MethodGet {
			state, err = editor.Read(r.Context(), token, id)
		} else {
			csrf := webCSRFProof(r)
			if err := admission.CheckMutation(r.Context(), token, csrf); err != nil {
				webFailure(w, err)
				return
			}
			var data struct {
				webRouterDraftInput
				ElementID string            `json:"elementId"`
				Action    string            `json:"action"`
				Values    map[string]string `json:"values"`
				Text      *string           `json:"text"`
			}
			if !readWebJSON(w, r, maxWebEditorBody, &data) {
				return
			}
			basis, ok := data.basis(id)
			if !ok || data.ElementID == "" {
				webResult(w, 400, "invalid_request", "")
				return
			}
			state, err = editor.Save(r.Context(), token, csrf, basis, data.ElementID, routersettings.ExclusionEdit{Action: data.Action, Values: data.Values, Text: data.Text})
		}
		if err != nil {
			webRouterVariableFailure(w, err)
			return
		}
		wire := map[string]any{"name": state.Name, "templateId": state.Basis.TemplateID, "elementId": state.Source.ElementID, "acceptedVersion": strconv.FormatInt(state.Basis.AcceptedVersion, 10), "activeGeneration": strconv.FormatInt(state.Basis.ActiveGeneration, 10), "draftGeneration": strconv.FormatInt(state.Basis.DraftGeneration, 10), "source": state.Source.Text, "variables": state.Source.Variables, "hasOverride": state.Override != nil}
		if state.Override != nil {
			wire["values"] = state.Override.Values
			wire["replacement"] = state.Override.Replacement
		}
		if state.File != nil {
			wire["file"] = string(state.File.Content)
			wire["bypassUnavailable"] = len(state.File.Content) > 0
		}
		if state.Issue != nil {
			wire["issue"] = state.Issue
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(wire)
	})
}
