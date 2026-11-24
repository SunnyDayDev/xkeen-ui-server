package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type webTemplateCopyService interface {
	Copy(context.Context, string, string, string, string, int64) (webadmin.TemplateSummary, error)
}

func NewWebTemplateCopyHandler(origin WebOrigin, service webTemplateCopyService, admission webPreparationAdmission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/templates/copy" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebEndpoint(w, r, origin, true, http.MethodPost) {
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		ids := query["templateId"]
		if err != nil || len(query) != 1 || len(ids) != 1 || ids[0] == "" || !utf8.ValidString(ids[0]) {
			webResult(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		token, proof := webSessionToken(r), webCSRFProof(r)
		if err := admission.CheckMutation(r.Context(), token, proof); err != nil {
			webFailure(w, err)
			return
		}
		var payload struct {
			Target  string `json:"targetId"`
			Version string `json:"version"`
		}
		if !readWebJSON(w, r, 8<<10, &payload) {
			return
		}
		version, err := strconv.ParseInt(payload.Version, 10, 64)
		if err != nil || version < 1 || strconv.FormatInt(version, 10) != payload.Version || payload.Target == "" {
			webResult(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		copied, err := service.Copy(r.Context(), token, proof, ids[0], payload.Target, version)
		switch {
		case errors.Is(err, templates.ErrConflict):
			webResult(w, http.StatusConflict, "template_exists", "")
		case errors.Is(err, templates.ErrVersionConflict):
			webResult(w, http.StatusConflict, "source_changed", "")
		case errors.Is(err, templates.ErrNotFound):
			webResult(w, http.StatusNotFound, "source_missing", "")
		case errors.Is(err, templates.ErrInvalid):
			webResult(w, http.StatusBadRequest, "invalid_request", "")
		case err != nil:
			webFailure(w, err)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(templateSummaryResponse(copied))
		}
	})
}
