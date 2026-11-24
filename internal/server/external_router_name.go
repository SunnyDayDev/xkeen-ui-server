package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

type externalRouterNameStore interface {
	ReadExternalRouterName(context.Context, externalaccess.Permit, string) (routersettings.DisplayNameSnapshot, error)
	WriteExternalRouterName(context.Context, externalaccess.Permit, routersettings.DisplayNameSnapshot, string) (routersettings.DisplayNameSnapshot, error)
}

func NewExternalRouterName(store externalRouterNameStore, next ExternalOperation) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		if r.URL.Path != "/api/v1/routers/name" {
			next(w, r, permit)
			return
		}
		w.Header().Set("Cache-Control", "no-store, no-transform")
		if r.Method != http.MethodGet && r.Method != http.MethodPatch {
			w.Header().Set("Allow", "GET, PATCH")
			externalError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		routerID, elementID, ok := routerElementQuery(r.URL.RawQuery)
		if !ok || elementID != "" {
			externalError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var expected, name string
		if r.Method == http.MethodPatch {
			if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
				externalFailure(w, err)
				return
			}
			expected, ok = externalCondition(w, r)
			if !ok {
				return
			}
			var input struct {
				Name *string `json:"name"`
			}
			if !readExternalTemplateJSON(w, r, 8<<10, &input) {
				return
			}
			if input.Name == nil || !routersettings.ValidDisplayName(*input.Name) {
				externalError(w, http.StatusBadRequest, "invalid_request")
				return
			}
			name = *input.Name
		}
		current, err := store.ReadExternalRouterName(r.Context(), permit, routerID)
		if err != nil {
			externalRouterNameFailure(w, err)
			return
		}
		if r.Method == http.MethodPatch {
			if expected != routerNameETag(current) {
				externalError(w, http.StatusPreconditionFailed, "precondition_failed")
				return
			}
			current, err = store.WriteExternalRouterName(r.Context(), permit, current, name)
			if err != nil {
				externalRouterNameFailure(w, err)
				return
			}
		}
		w.Header().Set("ETag", routerNameETag(current))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			RouterID string `json:"routerId"`
			Name     string `json:"name"`
		}{RouterID: current.RouterID, Name: current.Name})
	}
}

func routerNameETag(state routersettings.DisplayNameSnapshot) string {
	basis, _ := json.Marshal(state)
	return fmt.Sprintf(`"%x"`, sha256.Sum256(append([]byte("external-router-name\x00"), basis...)))
}

func externalRouterNameFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, routersettings.ErrVisibilityNotFound):
		externalError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, externalaccess.ErrConflict):
		externalError(w, http.StatusPreconditionFailed, "precondition_failed")
	default:
		externalFailure(w, err)
	}
}
