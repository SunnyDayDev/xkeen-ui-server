package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func NewExternalRouterIO(store externalRouterDraftReader, next ExternalOperation) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		if r.URL.Path != "/api/v1/routers/io" {
			next(w, r, permit)
			return
		}
		w.Header().Set("Cache-Control", "no-store, no-transform")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			externalError(w, 405, "method_not_allowed")
			return
		}
		id, element, ok := routerElementQuery(r.URL.RawQuery)
		if !ok || element != "" {
			externalError(w, 400, "invalid_request")
			return
		}
		snap, err := store.ReadExternalRouterDraft(r.Context(), permit, id)
		if err != nil {
			externalRouterFailure(w, err)
			return
		}
		inbounds, outbounds, selectionErr := publication.SelectDraftIO(snap.Accepted, snap.Editable)
		if !recheckExternalRouterDraft(w, r, store, permit, snap.Basis) {
			return
		}
		var invalid *publication.PreparationError
		switch {
		case errors.As(selectionErr, &invalid):
			externalError(w, 422, "inventory_unavailable")
			return
		case errors.Is(selectionErr, publication.ErrSourceMismatch):
			externalRouterFailure(w, routerdraft.ErrForeign)
			return
		case selectionErr != nil:
			externalRouterFailure(w, selectionErr)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Inbounds  []configuration.CatalogIOMember `json:"inbounds"`
			Outbounds []configuration.CatalogIOMember `json:"outbounds"`
		}{inbounds, outbounds})
	}
}

func recheckExternalRouterDraft(w http.ResponseWriter, r *http.Request, store externalRouterDraftReader, permit externalaccess.Permit, basis routerdraft.Basis) bool {
	current, err := store.ReadExternalRouterDraft(r.Context(), permit, basis.RouterID)
	if err != nil {
		externalRouterFailure(w, err)
		return false
	}
	if current.Basis != basis {
		externalError(w, 409, "snapshot_changed")
		return false
	}
	return true
}
