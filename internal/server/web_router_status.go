package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

type webRouterStatusService interface {
	List(context.Context, string) ([]webadmin.RouterStatus, error)
}

type webRouterStatus struct {
	RouterID   string             `json:"routerId"`
	Name       string             `json:"name"`
	TemplateID string             `json:"templateId"`
	Contact    reporting.Presence `json:"contact"`
	Desired    string             `json:"desiredRevision"`
	Applied    string             `json:"appliedRevision,omitempty"`
	Runtime    string             `json:"runtime,omitempty"`
}

func NewWebRouterStatusHandler(origin WebOrigin, service webRouterStatusService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/routers" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebEndpoint(w, r, origin, false, http.MethodGet) {
			return
		}
		rows, err := service.List(r.Context(), webSessionToken(r))
		if err != nil {
			webFailure(w, err)
			return
		}
		result := make([]webRouterStatus, len(rows))
		for i, row := range rows {
			result[i] = webRouterStatus{RouterID: row.RouterID, Name: row.Name, TemplateID: row.TemplateID,
				Contact: row.Contact, Desired: strconv.FormatInt(row.Desired, 10), Applied: optionalWebRevision(row.Applied), Runtime: row.Runtime}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Routers []webRouterStatus `json:"routers"`
		}{result})
	})
}
