package server

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"net/http"
	"strconv"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type webRouterReplacementService interface {
	Read(context.Context, string, string, string) (webadmin.RouterReplacement, error)
	Save(context.Context, string, string, webadmin.RouterDraftBasis, string, configuration.ContentAction) (webadmin.RouterReplacement, error)
}

func NewWebRouterReplacementHandler(origin WebOrigin, service webRouterReplacementService, admission webPreparationAdmission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/routers/replacement" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebEndpoint(w, r, origin, true, http.MethodGet, http.MethodPost) {
			return
		}
		routerID, elementID, ok := routerElementQuery(r.URL.RawQuery)
		if !ok || elementID == "" {
			webResult(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		token := webSessionToken(r)
		var result webadmin.RouterReplacement
		var err error
		if r.Method == http.MethodGet {
			result, err = service.Read(r.Context(), token, routerID, elementID)
		} else {
			csrf := webCSRFProof(r)
			if err := admission.CheckMutation(r.Context(), token, csrf); err != nil {
				webFailure(w, err)
				return
			}
			basis, action, ok := readReplacementChange(w, r, routerID)
			if !ok {
				return
			}
			result, err = service.Save(r.Context(), token, csrf, basis, elementID, action)
		}
		if err != nil {
			replacementFailure(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			RouterID         string                    `json:"routerId"`
			Name             string                    `json:"name"`
			TemplateID       string                    `json:"templateId"`
			AcceptedVersion  string                    `json:"acceptedVersion"`
			ActiveGeneration string                    `json:"activeGeneration"`
			DraftGeneration  string                    `json:"draftGeneration"`
			ElementID        string                    `json:"elementId"`
			Section          string                    `json:"section"`
			Mode             configuration.ContentMode `json:"mode"`
			HasOverride      bool                      `json:"hasOverride"`
			Source           *string                   `json:"source,omitempty"`
		}{
			RouterID: result.Basis.RouterID, Name: result.Name, TemplateID: result.Basis.TemplateID,
			AcceptedVersion:  strconv.FormatInt(result.Basis.AcceptedVersion, 10),
			ActiveGeneration: strconv.FormatInt(result.Basis.ActiveGeneration, 10),
			DraftGeneration:  strconv.FormatInt(result.Basis.DraftGeneration, 10),
			ElementID:        result.ElementID, Section: result.Section, Mode: result.Mode,
			HasOverride: result.HasOverride, Source: result.Source,
		})
	})
}

func readReplacementChange(w http.ResponseWriter, r *http.Request, id string) (webadmin.RouterDraftBasis, configuration.ContentAction, bool) {
	var payload struct {
		webRouterDraftInput
		Action configuration.ContentActionKind `json:"action"`
		Source json.RawMessage                 `json:"source"`
	}
	var emptyBasis webadmin.RouterDraftBasis
	var emptyAction configuration.ContentAction
	if !readWebJSON(w, r, maxWebEditorBody, &payload) {
		return emptyBasis, emptyAction, false
	}
	basis, ok := payload.basis(id)
	if ok {
		switch payload.Action {
		case configuration.ActivateReplacement:
			var source *string
			if payload.Source != nil && jsonv2.Unmarshal(payload.Source, &source) == nil && source != nil {
				return basis, configuration.ContentAction{Kind: payload.Action, Content: json.RawMessage(*source)}, true
			}
		case configuration.ReturnToVariables, configuration.ResetContentOverride:
			if payload.Source == nil {
				return basis, configuration.ContentAction{Kind: payload.Action}, true
			}
		}
	}
	webResult(w, http.StatusBadRequest, "invalid_request", "")
	return emptyBasis, emptyAction, false
}

func replacementFailure(w http.ResponseWriter, err error) {
	var invalid *routersettings.ReplacementSourceError
	if !errors.As(err, &invalid) {
		webRouterVariableFailure(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(struct {
		State  string `json:"state"`
		Code   string `json:"code"`
		Offset *int64 `json:"offset,omitempty"`
	}{State: "invalid_source", Code: invalid.Issue.Code, Offset: invalid.Issue.Offset})
}
