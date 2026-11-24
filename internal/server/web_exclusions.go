package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type webExclusionStore interface {
	ReadExclusions(context.Context, [32]byte, func() time.Time, string) (webadmin.ExclusionDraft, error)
	SaveExclusions(context.Context, [32]byte, func() time.Time, string, webadmin.ExclusionDraft) (webadmin.ExclusionDraft, error)
}

type exclusionWire struct {
	ID        string                       `json:"id"`
	Version   string                       `json:"version"`
	Text      *string                      `json:"text"`
	Variables []webadmin.ExclusionVariable `json:"variables"`
}

func NewWebExclusionsHandler(origin WebOrigin, store webExclusionStore, sessions *webadmin.Service, next http.Handler) http.Handler {
	const route = "/ui/v1/templates/xkeen-exclusions"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		preview := r.URL.Path == route+"/preview"
		if r.URL.Path != route && !preview {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebEndpoint(w, r, origin, true, http.MethodGet, http.MethodPost) {
			return
		}
		if preview && r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			webResult(w, 405, "method_not_allowed", "")
			return
		}
		id, _, ok := templateEditorQuery(r.URL.RawQuery, false)
		if !ok || !utf8.ValidString(id) {
			webResult(w, 400, "invalid_request", "")
			return
		}
		token, proof := webSessionToken(r), webCSRFProof(r)
		digest, _, err := webadmin.SessionProof(token, proof, r.Method == http.MethodPost)
		if err != nil {
			webFailure(w, err)
			return
		}
		if r.Method == http.MethodPost {
			if err = sessions.CheckMutation(r.Context(), token, proof); err != nil {
				webFailure(w, err)
				return
			}
		}
		base, err := store.ReadExclusions(r.Context(), digest, time.Now, id)
		if err != nil {
			webTemplateEditorFailure(w, err)
			return
		}
		if r.Method == http.MethodPost {
			var input exclusionWire
			if !readWebJSON(w, r, maxWebEditorBody, &input) {
				return
			}
			version, err := strconv.ParseInt(input.Version, 10, 64)
			if err != nil || version < 0 || strconv.FormatInt(version, 10) != input.Version || input.Text == nil || input.Variables == nil {
				webResult(w, 400, "invalid_request", "")
				return
			}
			if base.Version != version || base.ID != input.ID {
				webTemplateEditorFailure(w, templates.ErrVersionConflict)
				return
			}
			base.Text = *input.Text
			base.Variables = input.Variables
			if !base.Valid() {
				webResult(w, 422, "invalid_definitions", "")
				return
			}
			if preview {
				src := base.Source()
				file, issue := configuration.AssembleXKeenExclusions(&src, nil, nil)
				w.Header().Set("Content-Type", "application/json")
				if issue != nil {
					w.WriteHeader(422)
					_ = json.NewEncoder(w).Encode(struct {
						Code string `json:"code"`
						Line int    `json:"line"`
					}{issue.Code, issue.Line})
					return
				}
				_ = json.NewEncoder(w).Encode(struct {
					File string `json:"file"`
				}{string(file.Content)})
				return
			}
			base, err = store.SaveExclusions(r.Context(), digest, time.Now, id, base)
			if err != nil {
				webTemplateEditorFailure(w, err)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(exclusionWire{base.ID, strconv.FormatInt(base.Version, 10), &base.Text, base.Variables})
	})
}
