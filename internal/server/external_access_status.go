package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
)

type externalAccessStatusStore interface {
	ReadActiveMode(context.Context, externalaccess.Permit) (bool, error)
}

func NewExternalAccessStatus(store externalAccessStatusStore, next ExternalOperation) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		if r.URL.Path != "/api/v1/access" {
			next(w, r, permit)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			externalError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		if r.URL.RawQuery != "" || r.URL.ForceQuery {
			externalError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		protected, err := store.ReadActiveMode(r.Context(), permit)
		if err != nil {
			externalFailure(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Enabled   bool `json:"enabled"`
			Protected bool `json:"protected"`
		}{Enabled: true, Protected: protected})
	}
}
