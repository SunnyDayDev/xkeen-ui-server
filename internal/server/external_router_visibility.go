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

type externalRouterVisibilityStore interface {
	ReadExternalRouterVisibility(context.Context, externalaccess.Permit, string) (routersettings.VisibilitySnapshot, error)
	HideExternalRouter(context.Context, externalaccess.Permit, routersettings.VisibilitySnapshot) (routersettings.VisibilitySnapshot, error)
}

func NewExternalRouterVisibility(store externalRouterVisibilityStore, next ExternalOperation) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		if r.URL.Path != "/api/v1/routers/visibility" {
			next(w, r, permit)
			return
		}
		w.Header().Set("Cache-Control", "no-store, no-transform")
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, POST")
			externalError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		routerID, elementID, ok := routerElementQuery(r.URL.RawQuery)
		if !ok || elementID != "" {
			externalError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var expected string
		if r.Method == http.MethodPost {
			if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
				externalFailure(w, err)
				return
			}
			expected, ok = externalCondition(w, r)
			if !ok {
				return
			}
			var operation struct {
				Action  string `json:"action"`
				Confirm bool   `json:"confirm"`
			}
			if !readExternalTemplateJSON(w, r, 1024, &operation) {
				return
			}
			if operation.Action != "hide" || !operation.Confirm {
				externalError(w, http.StatusBadRequest, "invalid_request")
				return
			}
		}
		visibility, err := store.ReadExternalRouterVisibility(r.Context(), permit, routerID)
		if err != nil {
			externalVisibilityFailure(w, err)
			return
		}
		if r.Method == http.MethodPost {
			if expected != visibilityETag(visibility) {
				externalError(w, http.StatusPreconditionFailed, "precondition_failed")
				return
			}
			visibility, err = store.HideExternalRouter(r.Context(), permit, visibility)
			if err != nil {
				externalVisibilityFailure(w, err)
				return
			}
		}
		w.Header().Set("ETag", visibilityETag(visibility))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			RouterID string `json:"routerId"`
			Visible  bool   `json:"visible"`
		}{RouterID: visibility.RouterID, Visible: visibility.Visible})
	}
}

func visibilityETag(value routersettings.VisibilitySnapshot) string {
	basis, _ := json.Marshal(value)
	return fmt.Sprintf(`"%x"`, sha256.Sum256(append([]byte("external-router-visibility\x00"), basis...)))
}

func externalVisibilityFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, routersettings.ErrVisibilityNotFound):
		externalError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, externalaccess.ErrConflict):
		externalError(w, http.StatusPreconditionFailed, "precondition_failed")
	default:
		externalFailure(w, err)
	}
}
