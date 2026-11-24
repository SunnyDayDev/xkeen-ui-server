package server

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type webRouterVariableService interface {
	Read(context.Context, string, string, string) (webadmin.RouterVariables, error)
	Save(context.Context, string, string, webadmin.RouterDraftBasis, string, []routersettings.VariableChange) (webadmin.RouterVariables, error)
}

func NewWebRouterVariableHandler(origin WebOrigin, service webRouterVariableService, admission webPreparationAdmission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/routers/variables" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebEndpoint(w, r, origin, true, http.MethodGet, http.MethodPost) {
			return
		}
		routerID, elementID, ok := routerElementQuery(r.URL.RawQuery)
		if !ok || (r.Method == http.MethodPost && elementID == "") {
			webResult(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		token := webSessionToken(r)
		var result webadmin.RouterVariables
		var err error
		if r.Method == http.MethodGet {
			result, err = service.Read(r.Context(), token, routerID, elementID)
		} else {
			csrf := webCSRFProof(r)
			if err := admission.CheckMutation(r.Context(), token, csrf); err != nil {
				webFailure(w, err)
				return
			}
			basis, changes, ok := readVariableChanges(w, r, routerID)
			if !ok {
				return
			}
			result, err = service.Save(r.Context(), token, csrf, basis, elementID, changes)
		}
		if err != nil {
			webRouterVariableFailure(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			RouterID         string                            `json:"routerId"`
			Name             string                            `json:"name"`
			TemplateID       string                            `json:"templateId"`
			AcceptedVersion  string                            `json:"acceptedVersion"`
			ActiveGeneration string                            `json:"activeGeneration"`
			DraftGeneration  string                            `json:"draftGeneration"`
			Sections         []webadmin.RouterVariableSection  `json:"sections"`
			Selection        *webadmin.RouterVariableSelection `json:"selection,omitempty"`
		}{
			RouterID: result.Basis.RouterID, Name: result.Name, TemplateID: result.Basis.TemplateID,
			AcceptedVersion:  strconv.FormatInt(result.Basis.AcceptedVersion, 10),
			ActiveGeneration: strconv.FormatInt(result.Basis.ActiveGeneration, 10),
			DraftGeneration:  strconv.FormatInt(result.Basis.DraftGeneration, 10),
			Sections:         result.Sections, Selection: result.Selection,
		})
	})
}

func routerElementQuery(raw string) (routerID, elementID string, ok bool) {
	values, err := url.ParseQuery(raw)
	id := values.Get("routerId")
	if err != nil || len(values["routerId"]) != 1 || id == "" || !utf8.ValidString(id) {
		return "", "", false
	}
	if len(values) == 1 {
		return id, "", true
	}
	element := values.Get("elementId")
	if len(values) == 2 && len(values["elementId"]) == 1 && configuration.ValidElementID(element) {
		return id, element, true
	}
	return "", "", false
}

func readVariableChanges(w http.ResponseWriter, r *http.Request, id string) (webadmin.RouterDraftBasis, []routersettings.VariableChange, bool) {
	var payload struct {
		webRouterDraftInput
		Values []struct {
			Name   string          `json:"name"`
			Source json.RawMessage `json:"source"`
		} `json:"values"`
	}
	var empty webadmin.RouterDraftBasis
	if !readWebJSON(w, r, maxWebEditorBody, &payload) {
		return empty, nil, false
	}
	basis, ok := payload.basis(id)
	if !ok || len(payload.Values) == 0 {
		webResult(w, http.StatusBadRequest, "invalid_request", "")
		return empty, nil, false
	}
	changes := make([]routersettings.VariableChange, len(payload.Values))
	for i, item := range payload.Values {
		changes[i].Name = item.Name
		if item.Source != nil {
			var source *string
			if err := jsonv2.Unmarshal(item.Source, &source); err != nil || source == nil {
				webResult(w, http.StatusBadRequest, "invalid_request", "")
				return empty, nil, false
			}
			changes[i].Value = json.RawMessage(*source)
		}
	}
	return basis, changes, true
}

type webRouterDraftInput struct {
	TemplateID       string `json:"templateId"`
	AcceptedVersion  string `json:"acceptedVersion"`
	ActiveGeneration string `json:"activeGeneration"`
	DraftGeneration  string `json:"draftGeneration"`
}

func (input webRouterDraftInput) basis(routerID string) (webadmin.RouterDraftBasis, bool) {
	basis := webadmin.RouterDraftBasis{RouterID: routerID, TemplateID: input.TemplateID}
	var ok1, ok2, ok3 bool
	basis.AcceptedVersion, ok1 = variableGeneration(input.AcceptedVersion, 1)
	basis.ActiveGeneration, ok2 = variableGeneration(input.ActiveGeneration, 1)
	basis.DraftGeneration, ok3 = variableGeneration(input.DraftGeneration, 0)
	return basis, ok1 && ok2 && ok3 && input.TemplateID != ""
}

func variableGeneration(raw string, minimum int64) (int64, bool) {
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil && n >= minimum && strconv.FormatInt(n, 10) == raw
}

func webRouterVariableFailure(w http.ResponseWriter, err error) {
	var invalid *routersettings.VariableSourceError
	switch {
	case errors.Is(err, webadmin.ErrRouterDraftMissing):
		webResult(w, http.StatusNotFound, "router_or_element_unavailable", "")
	case errors.Is(err, webadmin.ErrRouterDraftForeign):
		webResult(w, http.StatusConflict, "different_template", "")
	case errors.Is(err, routersettings.ErrVariableMode):
		webResult(w, http.StatusConflict, "replacement_mode", "")
	case errors.Is(err, webadmin.ErrRouterDraftConflict), errors.Is(err, routersettings.ErrSharedScope):
		webResult(w, http.StatusConflict, "draft_conflict", "")
	case errors.As(err, &invalid):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(struct {
			State  string `json:"state"`
			Name   string `json:"name"`
			Code   string `json:"code"`
			Offset *int64 `json:"offset,omitempty"`
		}{State: "invalid_source", Name: invalid.Name, Code: invalid.Issue.Code, Offset: invalid.Issue.Offset})
	case errors.Is(err, routersettings.ErrInvalidDraft):
		webResult(w, http.StatusBadRequest, "invalid_request", "")
	default:
		webFailure(w, err)
	}
}
