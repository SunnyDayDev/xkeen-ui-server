package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
)

type externalRouterCatalogService interface {
	Read(context.Context, externalaccess.Permit, string, publication.RouterPreviewMode) (*publication.RouterCatalogPreview, error)
}

func NewExternalRouterCatalog(service externalRouterCatalogService, next ExternalOperation) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		if r.URL.Path != "/api/v1/routers/catalog" {
			next(w, r, permit)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			externalError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		id, mode, ok := routerCatalogQuery(r.URL.RawQuery)
		if !ok {
			externalError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		result, err := service.Read(r.Context(), permit, id, mode)
		if err != nil {
			externalRouterCatalogFailure(w, err)
			return
		}
		reply := externalCatalogReply{RouterID: result.Expected.RouterID, Source: result.Mode, Files: routerCatalogFiles(result)}
		if result.Mode == publication.PreviewDesired {
			reply.Evidence = "server_xray_validated"
			reply.DesiredRevision = strconv.FormatInt(result.Expected.DesiredNumber, 10)
		} else {
			reply.Evidence = "assembly_only"
			reply.TemplateID = result.Expected.TemplateID
			reply.AcceptedVersion = strconv.FormatInt(result.Expected.AcceptedVersion, 10)
			reply.ActiveGeneration = strconv.FormatInt(result.Expected.ActiveGeneration, 10)
			reply.DraftGeneration = strconv.FormatInt(*result.Expected.RouterDraftGeneration, 10)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	}
}

type externalCatalogReply struct {
	RouterID         string                        `json:"routerId"`
	Source           publication.RouterPreviewMode `json:"source"`
	Evidence         string                        `json:"evidence"`
	DesiredRevision  string                        `json:"desiredRevision,omitempty"`
	TemplateID       string                        `json:"templateId,omitempty"`
	AcceptedVersion  string                        `json:"acceptedVersion,omitempty"`
	ActiveGeneration string                        `json:"activeGeneration,omitempty"`
	DraftGeneration  string                        `json:"draftGeneration,omitempty"`
	Files            []routerCatalogFile           `json:"files"`
}

func externalRouterCatalogFailure(w http.ResponseWriter, err error) {
	var prepared *publication.PreparationError
	switch {
	case errors.As(err, &prepared):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(struct {
			Code   string                    `json:"code"`
			Issues []routerCatalogDiagnostic `json:"issues"`
		}{"catalog_unready", routerCatalogDiagnostics(prepared)})
	case errors.Is(err, publication.ErrInvalidCandidate):
		externalError(w, http.StatusUnprocessableEntity, "catalog_unready")
	case errors.Is(err, publication.ErrConflict):
		externalError(w, http.StatusConflict, "catalog_changed")
	case errors.Is(err, publication.ErrSourceMismatch):
		externalError(w, http.StatusConflict, "different_template")
	case errors.Is(err, publication.ErrSourceMissing), errors.Is(err, routerdraft.ErrMissing):
		externalError(w, http.StatusNotFound, "not_found")
	default:
		externalFailure(w, err)
	}
}
