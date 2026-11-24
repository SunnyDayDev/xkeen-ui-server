package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type webRouterPreparationService interface {
	Describe(context.Context, string, string) (webadmin.PreparationForm, error)
	Confirm(context.Context, string, string, webadmin.PreparationConfirmation) (webadmin.PreparedRouter, error)
	Prepared(context.Context, string, string) (webadmin.PreparedRouter, error)
}

type preparationVariable struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	DefaultSource string `json:"defaultSource,omitempty"`
	Required      bool   `json:"required"`
}
type preparationElement struct {
	Inactive  bool                  `json:"inactive,omitempty"`
	Section   string                `json:"section"`
	ElementID string                `json:"elementId"`
	Variables []preparationVariable `json:"variables"`
}
type preparationResponse struct {
	RouterID   string                         `json:"routerId"`
	TemplateID string                         `json:"templateId"`
	Source     webadmin.PreparationSourceKind `json:"source"`
	Version    string                         `json:"version"`
	Elements   []preparationElement           `json:"elements"`
}

type preparationConfirmation struct {
	RouterID   string                         `json:"routerId"`
	TemplateID string                         `json:"templateId"`
	Name       string                         `json:"name"`
	Source     webadmin.PreparationSourceKind `json:"source"`
	Version    string                         `json:"version"`
	Values     []struct {
		ElementID string `json:"elementId"`
		Name      string `json:"name"`
		Source    string `json:"source"`
	} `json:"values"`
}

func NewWebRouterPreparationHandler(lifetime context.Context, origin WebOrigin, service webRouterPreparationService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/routers/prepare" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebEndpoint(w, r, origin, r.Method == http.MethodGet, http.MethodGet, http.MethodPost) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			readWebPreparation(w, r, service)
		case http.MethodPost:
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			stop := context.AfterFunc(lifetime, cancel)
			defer stop()
			if lifetime.Err() != nil {
				cancel()
			}
			confirmWebPreparation(w, r.WithContext(ctx), service)
		}
	})
}

func readWebPreparation(w http.ResponseWriter, r *http.Request, service webRouterPreparationService) {
	params, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(params) != 1 {
		webResult(w, http.StatusBadRequest, "invalid_request", "")
		return
	}
	if ids := params["routerId"]; len(ids) == 1 && ids[0] != "" && utf8.ValidString(ids[0]) {
		outcome, err := service.Prepared(r.Context(), webSessionToken(r), ids[0])
		if err != nil {
			webPreparationFailure(w, err)
			return
		}
		writePreparedRouter(w, http.StatusOK, outcome)
		return
	}
	templates := params["templateId"]
	if len(templates) != 1 || templates[0] == "" || !utf8.ValidString(templates[0]) {
		webResult(w, http.StatusBadRequest, "invalid_request", "")
		return
	}
	form, err := service.Describe(r.Context(), webSessionToken(r), templates[0])
	if err != nil {
		webPreparationFailure(w, err)
		return
	}
	response := preparationResponse{
		RouterID: form.RouterID, TemplateID: form.TemplateID, Source: form.Kind, Version: strconv.FormatInt(form.Version, 10),
		Elements: make([]preparationElement, len(form.Elements)),
	}
	for i, element := range form.Elements {
		row := preparationElement{Section: element.Section, ElementID: element.ElementID, Inactive: element.Inactive, Variables: make([]preparationVariable, len(element.Variables))}
		for j, variable := range element.Variables {
			row.Variables[j] = preparationVariable(variable)
		}
		response.Elements[i] = row
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func confirmWebPreparation(w http.ResponseWriter, r *http.Request, service webRouterPreparationService) {
	var payload preparationConfirmation
	if !readWebJSON(w, r, 1<<20, &payload) {
		return
	}
	version, err := strconv.ParseInt(payload.Version, 10, 64)
	if err != nil || version < 1 || strconv.FormatInt(version, 10) != payload.Version || payload.TemplateID == "" || payload.RouterID == "" ||
		(payload.Source != webadmin.PreparationAccepted && payload.Source != webadmin.PreparationDraft) {
		webResult(w, http.StatusBadRequest, "invalid_request", "")
		return
	}
	values := make([]webadmin.PersonalInput, len(payload.Values))
	for i, value := range payload.Values {
		values[i] = webadmin.PersonalInput{ElementID: value.ElementID, Name: value.Name, Source: value.Source}
	}
	result, err := service.Confirm(r.Context(), webSessionToken(r), webCSRFProof(r), webadmin.PreparationConfirmation{
		RouterID: payload.RouterID, TemplateID: payload.TemplateID, Name: payload.Name, Kind: payload.Source, Version: version, Values: values,
	})
	if err != nil {
		webPreparationFailure(w, err)
		return
	}
	writePreparedRouter(w, http.StatusCreated, result)
}

func writePreparedRouter(w http.ResponseWriter, status int, outcome webadmin.PreparedRouter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		RouterID   string `json:"routerId"`
		Name       string `json:"name"`
		TemplateID string `json:"templateId"`
		Revision   string `json:"revision"`
	}{outcome.RouterID, outcome.Name, outcome.TemplateID, strconv.FormatInt(outcome.Revision, 10)})
}

func webPreparationFailure(w http.ResponseWriter, err error) {
	var input *webadmin.PreparationInputError
	var catalog *publication.PreparationError
	switch {
	case errors.As(err, &input):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(struct {
			State     string `json:"state"`
			Code      string `json:"code"`
			ElementID string `json:"elementId,omitempty"`
			Variable  string `json:"variable,omitempty"`
		}{"invalid_input", input.Code, input.ElementID, input.Variable})
	case errors.Is(err, webadmin.ErrInvalidPreparation):
		webResult(w, http.StatusBadRequest, "invalid_request", "")
	case errors.Is(err, webadmin.ErrPreparationSourceMissing):
		webResult(w, http.StatusNotFound, "source_missing", "")
	case errors.Is(err, webadmin.ErrPreparedRouterMissing):
		webResult(w, http.StatusNotFound, "not_prepared", "")
	case errors.Is(err, publication.ErrConflict):
		webResult(w, http.StatusConflict, "conflict", "")
	case errors.As(err, &catalog), errors.Is(err, publication.ErrInvalidCandidate):
		webResult(w, http.StatusUnprocessableEntity, "configuration_invalid", "")
	case errors.Is(err, publication.ErrCatalogRejected):
		webResult(w, http.StatusUnprocessableEntity, "catalog_rejected", "")
	case errors.Is(err, publication.ErrValidatorUnavailable):
		webResult(w, http.StatusServiceUnavailable, "validator_unavailable", "")
	default:
		webFailure(w, err)
	}
}
