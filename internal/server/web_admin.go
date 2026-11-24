package server

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/jsonvalue"
)

const webSessionCookie = "__Host-xkeen_session"

func NewWebAdminHandler(origin WebOrigin, sessions *webadmin.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := http.MethodPost
		switch r.URL.Path {
		case "/ui/v1/session":
			method = http.MethodGet
		case "/ui/v1/login", "/ui/v1/logout", "/ui/v1/session/activity":
		default:
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebRequest(w, r, origin, method) {
			return
		}
		token := webSessionToken(r)
		switch r.URL.Path {
		case "/ui/v1/session":
			configured, err := sessions.Configured(r.Context())
			switch {
			case err != nil:
				webFailure(w, err)
			case !configured:
				webResult(w, http.StatusOK, "setup_required", "")
			default:
				csrf, err := sessions.Session(r.Context(), token, "", webadmin.SessionRead)
				if errors.Is(err, webadmin.ErrSignedOut) {
					webResult(w, http.StatusOK, "signed_out", "")
				} else if err != nil {
					webFailure(w, err)
				} else {
					webResult(w, http.StatusOK, "active", csrf)
				}
			}
		case "/ui/v1/login":
			password, err := webLoginPassword(w, r)
			if err != nil {
				webResult(w, http.StatusBadRequest, "invalid_request", "")
				return
			}
			defer clear(password)
			issued, csrf, err := sessions.Login(r.Context(), password, token)
			if err != nil {
				webFailure(w, err)
				return
			}
			setWebCookie(w, issued, int(webadmin.AbsoluteLifetime.Seconds()))
			webResult(w, http.StatusOK, "active", csrf)
		default:
			proof := r.Header.Values("X-CSRF-Token")
			if len(proof) != 1 {
				webFailure(w, webadmin.ErrCSRF)
				return
			}
			action := webadmin.SessionTouch
			if r.URL.Path == "/ui/v1/logout" {
				action = webadmin.SessionRevoke
			}
			csrf, err := sessions.Session(r.Context(), token, proof[0], action)
			if err != nil {
				webFailure(w, err)
				return
			}
			if action == webadmin.SessionRevoke {
				setWebCookie(w, "", -1)
				webResult(w, http.StatusOK, "signed_out", "")
				return
			}
			webResult(w, http.StatusOK, "active", csrf)
		}
	})
}

func allowWebRequest(w http.ResponseWriter, r *http.Request, origin WebOrigin, methods ...string) bool {
	return allowWebEndpoint(w, r, origin, false, methods...)
}

func allowWebEndpoint(w http.ResponseWriter, r *http.Request, origin WebOrigin, hasQuery bool, methods ...string) bool {
	w.Header().Set("Cache-Control", "no-store")
	if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		webResult(w, http.StatusServiceUnavailable, "unavailable", "")
		return false
	}
	if !hasQuery && (r.URL.RawQuery != "" || r.URL.ForceQuery) {
		webResult(w, http.StatusBadRequest, "invalid_request", "")
		return false
	}
	if !slices.Contains(methods, r.Method) {
		w.Header().Set("Allow", strings.Join(methods, ", "))
		webResult(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return false
	}
	if !origin.allows(r, r.Method != http.MethodGet && r.Method != http.MethodHead) {
		webResult(w, http.StatusForbidden, "invalid_origin", "")
		return false
	}
	return true
}

func webSessionToken(r *http.Request) string {
	if cookies := r.CookiesNamed(webSessionCookie); len(cookies) == 1 {
		return cookies[0].Value
	}
	return ""
}

func webLoginPassword(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	bad := errors.New("invalid login body")
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if len(r.Header.Values("Content-Type")) != 1 || err != nil || media != "application/json" || len(params) != 0 {
		return nil, bad
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<10))
	defer clear(body)
	if err != nil {
		return nil, bad
	}
	parsed, issue := jsonvalue.Parse(body)
	object, _ := parsed.(map[string]any)
	password, ok := object["password"].(string)
	if issue != nil || len(object) != 1 || !ok {
		return nil, bad
	}
	return []byte(password), nil
}

func setWebCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: webSessionCookie, Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}

func webResult(w http.ResponseWriter, status int, state, csrf string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		State string `json:"state"`
		CSRF  string `json:"csrf,omitempty"`
	}{State: state, CSRF: csrf})
}

func webFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, webadmin.ErrSetupRequired):
		webResult(w, http.StatusConflict, "setup_required", "")
	case errors.Is(err, webadmin.ErrInvalidCredentials):
		webResult(w, http.StatusUnauthorized, "invalid_credentials", "")
	case errors.Is(err, webadmin.ErrSignedOut):
		webResult(w, http.StatusUnauthorized, "signed_out", "")
	case errors.Is(err, webadmin.ErrCSRF):
		webResult(w, http.StatusForbidden, "invalid_csrf", "")
	case errors.Is(err, webadmin.ErrRateLimited):
		w.Header().Set("Retry-After", "30")
		webResult(w, http.StatusTooManyRequests, "rate_limited", "")
	default:
		webResult(w, http.StatusServiceUnavailable, "unavailable", "")
	}
}
