package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type webRouterInboundService interface {
	Read(context.Context, string, string, string) (webadmin.RouterInboundView, error)
	Change(context.Context, string, string, webadmin.RouterDraftBasis, string, configuration.InboundAction) (int64, error)
}

func NewWebRouterInboundHandler(origin WebOrigin, service webRouterInboundService, admission webPreparationAdmission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/routers/inbounds" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebEndpoint(w, r, origin, true, http.MethodGet, http.MethodPost) {
			return
		}
		routerID, selected, ok := routerElementQuery(r.URL.RawQuery)
		if !ok || (r.Method == http.MethodPost && selected == "") {
			webResult(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		token := webSessionToken(r)
		switch r.Method {
		case http.MethodGet:
			result, err := service.Read(r.Context(), token, routerID, selected)
			if err != nil {
				webInboundFailure(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				RouterID         string                          `json:"routerId"`
				TemplateID       string                          `json:"templateId"`
				Name             string                          `json:"name"`
				AcceptedVersion  string                          `json:"acceptedVersion"`
				ActiveGeneration string                          `json:"activeGeneration"`
				DraftGeneration  string                          `json:"draftGeneration"`
				Inbounds         []publication.DraftInbound      `json:"inbounds"`
				Selected         string                          `json:"selected"`
				Dependencies     []publication.InboundDependency `json:"dependencies"`
			}{
				RouterID: result.Basis.RouterID, TemplateID: result.Basis.TemplateID, Name: result.Name,
				AcceptedVersion:  strconv.FormatInt(result.Basis.AcceptedVersion, 10),
				ActiveGeneration: strconv.FormatInt(result.Basis.ActiveGeneration, 10),
				DraftGeneration:  strconv.FormatInt(result.Basis.DraftGeneration, 10),
				Inbounds:         result.Items, Selected: result.Selected, Dependencies: result.Dependencies,
			})
		case http.MethodPost:
			csrf := webCSRFProof(r)
			if err := admission.CheckMutation(r.Context(), token, csrf); err != nil {
				webFailure(w, err)
				return
			}
			var input struct {
				webRouterDraftInput
				Action configuration.InboundAction `json:"action"`
			}
			if !readWebJSON(w, r, maxWebEditorBody, &input) {
				return
			}
			basis, ok := input.basis(routerID)
			if !ok || (input.Action != configuration.InboundActionDisable && input.Action != configuration.InboundActionRemove) {
				webResult(w, http.StatusBadRequest, "invalid_request", "")
				return
			}
			generation, err := service.Change(r.Context(), token, csrf, basis, selected, input.Action)
			if err != nil {
				webInboundFailure(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				State           string `json:"state"`
				DraftGeneration string `json:"draftGeneration"`
			}{"saved", strconv.FormatInt(generation, 10)})
		}
	})
}

func webInboundFailure(w http.ResponseWriter, err error) {
	var dependency *publication.InboundInUse
	var invalid *publication.PreparationError
	switch {
	case errors.As(err, &dependency):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(struct {
			State        string                          `json:"state"`
			Dependencies []publication.InboundDependency `json:"dependencies"`
		}{"inbound_in_use", dependency.Dependencies})
	case errors.As(err, &invalid):
		webResult(w, http.StatusConflict, "dependencies_unavailable", "")
	case errors.Is(err, publication.ErrInvalidCandidate):
		webResult(w, http.StatusNotFound, "router_or_element_unavailable", "")
	case errors.Is(err, publication.ErrConflict):
		webResult(w, http.StatusConflict, "draft_conflict", "")
	default:
		webRouterVariableFailure(w, err)
	}
}
