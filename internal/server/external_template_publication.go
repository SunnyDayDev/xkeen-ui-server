package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
)

type externalTemplatePublicationService interface {
	Preview(context.Context, externalaccess.Permit, string, int64) (*externalaccess.TemplateGroupPreview, error)
	Confirm(context.Context, externalaccess.Permit, string, string) (*publication.TemplateGroupResult, error)
	Current(context.Context, externalaccess.Permit, string) (*publication.TemplateGroupCurrent, error)
}

func NewExternalTemplatePublications(lifetime context.Context, service externalTemplatePublicationService, next ExternalOperation) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		const root = "/api/v1/templates/publication"
		var method string
		switch r.URL.Path {
		case root:
			method = http.MethodGet
		case root + "/preview", root + "/confirm":
			method = http.MethodPost
		default:
			next(w, r, permit)
			return
		}
		if r.Method != method {
			w.Header().Set("Allow", method)
			externalError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		id, ok := publicationTemplateID(r.URL.RawQuery)
		if !ok {
			externalError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		ctx, done := publicationRequestContext(r.Context(), lifetime)
		defer done()
		var reply webPublicationResponse
		switch r.URL.Path {
		case root:
			result, err := service.Current(ctx, permit, id)
			if err != nil {
				externalTemplatePublicationFailure(w, err)
				return
			}
			reply = templatePublicationCurrentResponse(result)
		case root + "/preview":
			var input struct {
				Version string `json:"version"`
			}
			if !readExternalPublicationJSON(w, r, 1024, &input) {
				return
			}
			version, ok := variableGeneration(input.Version, 1)
			if !ok {
				externalError(w, http.StatusBadRequest, "invalid_request")
				return
			}
			result, err := service.Preview(ctx, permit, id, version)
			if err != nil {
				externalTemplatePublicationFailure(w, err)
				return
			}
			reply, err = templatePublicationPreviewResponse(result.Plan, result.Contact, result.Receipt)
			if err != nil {
				externalFailure(w, err)
				return
			}
		case root + "/confirm":
			var input struct {
				Receipt string `json:"receipt"`
			}
			if !readExternalPublicationJSON(w, r, 16<<10, &input) {
				return
			}
			if input.Receipt == "" {
				externalError(w, http.StatusBadRequest, "invalid_request")
				return
			}
			result, err := service.Confirm(ctx, permit, id, input.Receipt)
			if err != nil {
				externalTemplatePublicationFailure(w, err)
				return
			}
			reply = templatePublicationConfirmedResponse(result)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	}
}

func readExternalPublicationJSON(w http.ResponseWriter, r *http.Request, max int64, input any) bool {
	if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		externalFailure(w, err)
		return false
	}
	return readExternalTemplateJSON(w, r, max, input)
}

func externalTemplatePublicationFailure(w http.ResponseWriter, err error) {
	var invalid *publication.PreparationError
	switch {
	case errors.Is(err, publication.ErrConflict), errors.Is(err, publication.ErrGroupPreviewChanged):
		externalError(w, http.StatusConflict, "preview_changed")
	case errors.Is(err, publication.ErrSourceMissing):
		externalError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, publication.ErrCatalogRejected):
		externalError(w, http.StatusUnprocessableEntity, "catalog_rejected")
	case errors.Is(err, publication.ErrValidatorUnavailable):
		externalError(w, http.StatusServiceUnavailable, "validator_unavailable")
	case errors.Is(err, publication.ErrBypassUnavailable):
		externalError(w, http.StatusUnprocessableEntity, "bypass_unavailable")
	case errors.Is(err, publication.ErrInvalidCandidate), errors.As(err, &invalid):
		externalError(w, http.StatusUnprocessableEntity, "catalog_unready")
	default:
		externalFailure(w, err)
	}
}
