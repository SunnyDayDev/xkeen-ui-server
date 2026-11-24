package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

func NewWebRouterNameHandler(origin WebOrigin, service webadmin.RouterName, admission webPreparationAdmission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/routers/name" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebEndpoint(w, r, origin, true, http.MethodGet, http.MethodPost) {
			return
		}
		routerID, element, ok := routerElementQuery(r.URL.RawQuery)
		if !ok || element != "" {
			webResult(w, 400, "invalid_request", "")
			return
		}
		token := webSessionToken(r)
		var snapshot routersettings.DisplayNameSnapshot
		var err error
		if r.Method == http.MethodGet {
			snapshot, err = service.Read(r.Context(), token, routerID)
		} else {
			csrf := webCSRFProof(r)
			if err := admission.CheckMutation(r.Context(), token, csrf); err != nil {
				webFailure(w, err)
				return
			}
			var input struct {
				ActiveGeneration string `json:"activeGeneration"`
				Version          string `json:"version"`
				Name             string `json:"name"`
			}
			if !readWebJSON(w, r, 8<<10, &input) {
				return
			}
			generation, genErr := strconv.ParseInt(input.ActiveGeneration, 10, 64)
			version, versionErr := strconv.ParseInt(input.Version, 10, 64)
			if genErr != nil || versionErr != nil || generation < 1 || version < 0 ||
				strconv.FormatInt(generation, 10) != input.ActiveGeneration || strconv.FormatInt(version, 10) != input.Version {
				webResult(w, 400, "invalid_request", "")
				return
			}
			if !routersettings.ValidDisplayName(input.Name) {
				webResult(w, 400, "invalid_name", "")
				return
			}
			snapshot, err = service.Save(r.Context(), token, csrf, routersettings.DisplayNameSnapshot{RouterID: routerID, ActiveGeneration: generation, Version: version}, input.Name)
		}
		switch {
		case errors.Is(err, routersettings.ErrVisibilityNotFound):
			webResult(w, 404, "not_found", "")
		case errors.Is(err, routersettings.ErrDisplayNameConflict):
			webResult(w, 409, "name_changed", "")
		case errors.Is(err, routersettings.ErrInvalidDraft):
			webResult(w, 400, "invalid_name", "")
		case err != nil:
			webFailure(w, err)
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				RouterID         string `json:"routerId"`
				ActiveGeneration string `json:"activeGeneration"`
				Version          string `json:"version"`
				Name             string `json:"name"`
			}{snapshot.RouterID, strconv.FormatInt(snapshot.ActiveGeneration, 10), strconv.FormatInt(snapshot.Version, 10), snapshot.Name})
		}
	})
}
