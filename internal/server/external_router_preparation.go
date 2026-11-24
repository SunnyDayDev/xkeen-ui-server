package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerpreparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func NewExternalRouterPreparation(lifetime context.Context, service externalaccess.RouterPreparation, next ExternalOperation) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		if r.URL.Path != "/api/v1/routers/preparation" {
			next(w, r, permit)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, POST")
			externalError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		params, err := url.ParseQuery(r.URL.RawQuery)
		router, template := params.Get("routerId"), params.Get("templateId")
		if err != nil || len(params) != 2 || len(params["routerId"]) != 1 || len(params["templateId"]) != 1 ||
			!configuration.ValidElementID(router) || template == "" || !utf8.ValidString(template) {
			externalError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if r.Method == http.MethodGet {
			inspectExternalPreparation(w, r, service, permit, router, template)
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		stop := context.AfterFunc(lifetime, cancel)
		defer stop()
		if lifetime.Err() != nil {
			cancel()
		}
		prepareExternalRouter(w, r.WithContext(ctx), service, permit, router, template)
	}
}

func inspectExternalPreparation(w http.ResponseWriter, r *http.Request, service externalaccess.RouterPreparation,
	permit externalaccess.Permit, router, template string) {
	snapshot, err := service.Inspect(r.Context(), permit, router, template)
	if err != nil {
		externalPreparationFailure(w, err)
		return
	}
	body, tag, err := externalPreparationDocument(permit, snapshot.View)
	if err != nil {
		externalFailure(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", tag)
	_, _ = w.Write(body)
}

func prepareExternalRouter(w http.ResponseWriter, r *http.Request, service externalaccess.RouterPreparation,
	permit externalaccess.Permit, router, template string) {
	if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		externalFailure(w, err)
		return
	}
	expected, ok := externalCondition(w, r)
	if !ok {
		return
	}
	var command struct {
		Action string `json:"action"`
		Name   string `json:"name"`
		Values []struct {
			ElementID string `json:"elementId"`
			Name      string `json:"name"`
			Source    string `json:"source"`
		} `json:"values"`
	}
	if !readExternalTemplateJSON(w, r, maxWebEditorBody, &command) {
		return
	}
	if command.Action != "prepare" {
		externalError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	snapshot, err := service.Inspect(r.Context(), permit, router, template)
	if err != nil {
		externalPreparationFailure(w, err)
		return
	}
	_, tag, err := externalPreparationDocument(permit, snapshot.View)
	if err != nil {
		externalFailure(w, err)
		return
	}
	if tag != expected {
		externalPreparationFailure(w, publication.ErrConflict)
		return
	}
	values := make([]routerpreparation.PersonalInput, len(command.Values))
	for i, item := range command.Values {
		values[i] = routerpreparation.PersonalInput{ElementID: item.ElementID, Name: item.Name, Source: item.Source}
	}
	receipt, err := service.Prepare(r.Context(), permit, snapshot, command.Name, values)
	if err != nil {
		externalPreparationFailure(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(receipt)
}

func externalPreparationDocument(permit externalaccess.Permit, view externalaccess.PreparationView) ([]byte, string, error) {
	return externalRouterDocument(fmt.Sprintf("external-router-preparation:%d", permit.Version), view)
}

func externalPreparationFailure(w http.ResponseWriter, err error) {
	var invalidCatalog *publication.PreparationError
	switch {
	case errors.Is(err, externalaccess.ErrPreparationMissing):
		externalError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, publication.ErrConflict):
		externalError(w, http.StatusPreconditionFailed, "precondition_failed")
	case errors.Is(err, routerpreparation.ErrInvalidPreparation), errors.Is(err, publication.ErrInvalidCandidate):
		externalError(w, http.StatusUnprocessableEntity, "invalid_input")
	case errors.As(err, &invalidCatalog), errors.Is(err, publication.ErrCatalogRejected):
		externalError(w, http.StatusUnprocessableEntity, "catalog_rejected")
	case errors.Is(err, publication.ErrValidatorUnavailable):
		externalError(w, http.StatusServiceUnavailable, "validator_unavailable")
	default:
		externalFailure(w, err)
	}
}
