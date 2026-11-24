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
	"strconv"
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
					externalError(w, 403, "invalid_origin")
					return
				}
			}
			if operation == nil {
				externalError(w, 404, "not_found")
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
		externalError(w, 404, "not_found")
	case errors.Is(err, externalaccess.ErrUnauthorized):
		w.Header().Set("WWW-Authenticate", "Bearer")
		externalError(w, 401, "unauthorized")
	case errors.Is(err, externalaccess.ErrConflict):
		externalError(w, 409, "conflict")
	default:
		externalError(w, 503, "unavailable")
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

func externalError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code string `json:"code"`
	}{code})
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
		var versionText string
		if rotate {
			var in struct {
				Version string `json:"version"`
			}
			if !readExternalJSON(w, r, 4096, &in) {
				webResult(w, 400, "invalid_request", "")
				return
			}
			versionText = in.Version
			change = &externalaccess.Change{Rotate: true}
		} else {
			var in struct {
				Version   string `json:"version"`
				Enabled   *bool  `json:"enabled"`
				Protected *bool  `json:"protected"`
			}
			if !readExternalJSON(w, r, 4096, &in) || in.Enabled == nil || in.Protected == nil {
				webResult(w, 400, "invalid_request", "")
				return
			}
			versionText = in.Version
			change = &externalaccess.Change{Enabled: *in.Enabled, Protected: *in.Protected}
		}
		version, ok := parseAccessVersion(versionText)
		if !ok {
			webResult(w, 400, "invalid_request", "")
			return
		}
		change.Version = version
	}
	state, secret, err := store.Owner(r.Context(), digest, change)
	if err != nil {
		if errors.Is(err, externalaccess.ErrConflict) {
			webResult(w, 409, "conflict", "")
		} else {
			webFailure(w, err)
		}
		return
	}
	writeOwnerSettings(w, state, secret)
}
func parseAccessVersion(s string) (int64, bool) {
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err == nil && v > 0 && strconv.FormatInt(v, 10) == s
}
func writeOwnerSettings(w http.ResponseWriter, state externalaccess.Settings, secret string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Enabled   bool   `json:"enabled"`
		Protected bool   `json:"protected"`
		HasToken  bool   `json:"hasToken"`
		Version   string `json:"version"`
		Token     string `json:"token,omitempty"`
	}{state.Enabled, state.Protected, state.HasToken, strconv.FormatInt(state.Version, 10), secret})
}
