package server

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type webTemplateService interface {
	List(context.Context, string) ([]webadmin.TemplateSummary, error)
	Create(context.Context, string, string, string) (webadmin.TemplateSummary, error)
}

type webTemplateSummary struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

func templateSummaryResponse(summary webadmin.TemplateSummary) webTemplateSummary {
	return webTemplateSummary{ID: summary.ID, Version: strconv.FormatInt(summary.Version, 10)}
}

func NewWebTemplatesHandler(origin WebOrigin, service webTemplateService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/templates" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebRequest(w, r, origin, http.MethodGet, http.MethodPost) {
			return
		}
		if r.Method == http.MethodPost {
			createWebTemplate(w, r, service)
			return
		}
		summaries, err := service.List(r.Context(), webSessionToken(r))
		if err != nil {
			webFailure(w, err)
			return
		}
		writeTemplateSummaries(w, summaries)
	})
}

func createWebTemplate(w http.ResponseWriter, r *http.Request, service webTemplateService) {
	var input struct {
		ID string `json:"id"`
	}
	if !readWebJSON(w, r, 8<<10, &input) {
		return
	}
	if input.ID == "" {
		webResult(w, http.StatusBadRequest, "invalid_request", "")
		return
	}
	saved, err := service.Create(r.Context(), webSessionToken(r), webCSRFProof(r), input.ID)
	switch {
	case errors.Is(err, templates.ErrConflict):
		webResult(w, http.StatusConflict, "template_exists", "")
	case errors.Is(err, templates.ErrInvalid):
		webResult(w, http.StatusBadRequest, "invalid_request", "")
	case err != nil:
		webFailure(w, err)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(templateSummaryResponse(saved))
	}
}

func webCSRFProof(r *http.Request) string {
	if values := r.Header.Values("X-CSRF-Token"); len(values) == 1 {
		return values[0]
	}
	return ""
}

func readWebJSON(w http.ResponseWriter, r *http.Request, limit int64, out any) bool {
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if len(r.Header.Values("Content-Type")) != 1 || err != nil || media != "application/json" || len(params) != 0 {
		webResult(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "")
		return false
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	var sizeError *http.MaxBytesError
	if errors.As(err, &sizeError) {
		webResult(w, http.StatusRequestEntityTooLarge, "request_too_large", "")
		return false
	}
	if err != nil || jsonv2.Unmarshal(raw, out, jsonv2.RejectUnknownMembers(true)) != nil {
		webResult(w, http.StatusBadRequest, "invalid_request", "")
		return false
	}
	return true
}

func writeTemplateSummaries(w http.ResponseWriter, summaries []templates.Summary) {
	result := make([]webTemplateSummary, len(summaries))
	for i, summary := range summaries {
		result[i] = templateSummaryResponse(summary)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Templates []webTemplateSummary `json:"templates"`
	}{Templates: result})
}
