package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type webTemplateLister interface {
	List(context.Context, string) ([]webadmin.TemplateSummary, error)
}

func NewWebTemplatesHandler(origin WebOrigin, lister webTemplateLister, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/templates" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebRequest(w, r, origin, http.MethodGet) {
			return
		}
		summaries, err := lister.List(r.Context(), webSessionToken(r))
		if err != nil {
			webFailure(w, err)
			return
		}
		type item struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		}
		result := make([]item, len(summaries))
		for i, summary := range summaries {
			result[i] = item{ID: summary.ID, Version: strconv.FormatInt(summary.Version, 10)}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Templates []item `json:"templates"`
		}{Templates: result})
	})
}
