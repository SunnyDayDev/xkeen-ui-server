package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

func NewWebRouterVisibilityHandler(origin WebOrigin, service webadmin.RouterVisibility, admission webPreparationAdmission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/routers/visibility" {
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
		var snapshot routersettings.VisibilitySnapshot
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
				Epoch            string `json:"epoch"`
				Confirm          bool   `json:"confirm"`
			}
			if !readWebJSON(w, r, 1024, &input) {
				return
			}
			generation, genErr := strconv.ParseInt(input.ActiveGeneration, 10, 64)
			epoch, epochErr := strconv.ParseInt(input.Epoch, 10, 64)
			if !input.Confirm || genErr != nil || epochErr != nil || generation < 1 || epoch < 0 ||
				strconv.FormatInt(generation, 10) != input.ActiveGeneration || strconv.FormatInt(epoch, 10) != input.Epoch {
				webResult(w, 400, "invalid_request", "")
				return
			}
			snapshot, err = service.Hide(r.Context(), token, csrf, routersettings.VisibilitySnapshot{RouterID: routerID, ActiveGeneration: generation, Epoch: epoch, Visible: true})
		}
		switch {
		case errors.Is(err, routersettings.ErrVisibilityNotFound):
			webResult(w, 404, "not_found", "")
		case errors.Is(err, routersettings.ErrVisibilityConflict):
			webResult(w, 409, "visibility_changed", "")
		case err != nil:
			webFailure(w, err)
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				RouterID         string `json:"routerId"`
				ActiveGeneration string `json:"activeGeneration"`
				Epoch            string `json:"epoch"`
				Visible          bool   `json:"visible"`
			}{snapshot.RouterID, strconv.FormatInt(snapshot.ActiveGeneration, 10), strconv.FormatInt(snapshot.Epoch, 10), snapshot.Visible})
		}
	})
}
