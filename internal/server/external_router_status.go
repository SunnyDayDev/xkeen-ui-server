package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

type externalRouterStatusStore interface {
	ReadExternalRouterStatuses(context.Context, externalaccess.Permit) ([]reporting.RouterStatus, error)
}

func NewExternalRouterStatuses(store externalRouterStatusStore, next ExternalOperation) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		if r.URL.Path != "/api/v1/routers" {
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
		rows, err := store.ReadExternalRouterStatuses(r.Context(), permit)
		if err != nil {
			externalFailure(w, err)
			return
		}
		type overview struct {
			webRouterStatus
			Recovery string `json:"recovery,omitempty"`
		}
		result := make([]overview, len(rows))
		for i, row := range rows {
			result[i] = overview{webRouterStatus: webRouterStatus{RouterID: row.RouterID, Name: row.Name, TemplateID: row.TemplateID,
				Contact: row.Contact, Desired: strconv.FormatInt(row.Desired, 10), Applied: optionalWebRevision(row.Applied), Runtime: row.Runtime},
				Recovery: row.Recovery}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Routers []overview `json:"routers"`
		}{result})
	}
}
