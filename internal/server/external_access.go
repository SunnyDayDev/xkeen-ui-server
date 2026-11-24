package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

const externalSettingsPath = "/ui/v1/settings/external-api"

type ExternalAccessStore interface {
	Admit(context.Context, []string) (externalaccess.Permit, error)
	Owner(context.Context, [32]byte, *externalaccess.Change) (externalaccess.Settings, string, error)
}
type ExternalOperation func(http.ResponseWriter, *http.Request, externalaccess.Permit)

func NewExternalAccessHandler(origin WebOrigin, store ExternalAccessStore, operation ExternalOperation, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case origin != "" && (path == externalSettingsPath || path == externalSettingsPath+"/token"):
			ownerExternalSettings(w, r, origin, store)
		case path == "/api/v1" || strings.HasPrefix(path, "/api/v1/"):
			w.Header().Set("Cache-Control", "no-store")
			if r.TLS == nil || r.TLS.Version < tls.VersionTLS12 {
				externalFailure(w, externalaccess.ErrDisabled)
				return
			}
			permit, err := store.Admit(r.Context(), r.Header.Values("Authorization"))
			if err != nil {
				externalFailure(w, err)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				actual, err := ParseWebOrigin("https://" + r.Host)
				if err != nil || !actual.allows(r, false) {
					webResult(w, 403, "invalid_origin", "")
					return
				}
			}
			if operation == nil {
				webResult(w, 404, "not_found", "")
				return
			}
			operation(w, r, permit)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func externalFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, externalaccess.ErrDisabled):
		webResult(w, 404, "not_found", "")
	case errors.Is(err, externalaccess.ErrUnauthorized):
		w.Header().Set("WWW-Authenticate", "Bearer")
		webResult(w, 401, "unauthorized", "")
	case errors.Is(err, externalaccess.ErrConflict):
		webResult(w, 409, "conflict", "")
	default:
		webFailure(w, err)
	}
}

func readExternalJSON(w http.ResponseWriter, r *http.Request, max int64, out any) bool {
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if len(r.Header.Values("Content-Type")) != 1 || err != nil || mt != "application/json" || len(params) != 0 {
		return false
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
	if err != nil {
		return false
	}
	return jsonv2.Unmarshal(raw, out, jsonv2.RejectUnknownMembers(true)) == nil
}

func ownerExternalSettings(w http.ResponseWriter, r *http.Request, origin WebOrigin, store ExternalAccessStore) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		webResult(w, 400, "invalid_request", "")
		return
	}
	rotate := r.URL.Path == externalSettingsPath+"/token"
	mutation := r.Method != http.MethodGet
	if rotate && r.Method != http.MethodPost || !rotate && r.Method != http.MethodPatch && mutation {
		allow := "GET, PATCH"
		if rotate {
			allow = "POST"
		}
		w.Header().Set("Allow", allow)
		webResult(w, 405, "method_not_allowed", "")
		return
	}
	if !origin.allows(r, mutation) {
		webResult(w, 403, "invalid_origin", "")
		return
	}
	var token, proof string
	if cookies := r.CookiesNamed(webSessionCookie); len(cookies) == 1 {
		token = cookies[0].Value
	}
	if values := r.Header.Values("X-CSRF-Token"); len(values) == 1 {
		proof = values[0]
	}
	digest, _, err := webadmin.SessionProof(token, proof, mutation)
	if err != nil {
		webFailure(w, err)
		return
	}
	var change *externalaccess.Change
	if mutation {
		if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			webFailure(w, err)
			return
		}
		if rotate {
			var in struct {
				Version int64 `json:"version"`
			}
			if !readExternalJSON(w, r, 4096, &in) || in.Version < 1 {
				webResult(w, 400, "invalid_request", "")
				return
			}
			change = &externalaccess.Change{Version: in.Version, Rotate: true}
		} else {
			var in struct {
				Version   int64 `json:"version"`
				Enabled   *bool `json:"enabled"`
				Protected *bool `json:"protected"`
			}
			if !readExternalJSON(w, r, 4096, &in) || in.Version < 1 || in.Enabled == nil || in.Protected == nil {
				webResult(w, 400, "invalid_request", "")
				return
			}
			change = &externalaccess.Change{Version: in.Version, Enabled: *in.Enabled, Protected: *in.Protected}
		}
	}
	state, secret, err := store.Owner(r.Context(), digest, change)
	if err != nil {
		externalFailure(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		externalaccess.Settings
		Token string `json:"token,omitempty"`
	}{state, secret})
}
