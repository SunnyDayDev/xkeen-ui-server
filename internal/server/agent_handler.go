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

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
)

const maxEnrollmentBody = 1024

type AgentAuthenticator interface {
	Enroll(context.Context, string, string) (agentcredentials.Identity, error)
	Identity(context.Context, string) (agentcredentials.Identity, error)
}

// NewAgentHandler adds the agent endpoints to the existing health endpoint.
// A directly established TLS connection is required before invoking the port.
func NewAgentHandler(authenticator AgentAuthenticator, desired DesiredRevisionReader, contacts ContactService) http.Handler {
	health := NewHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/agent/v1/enroll", "/agent/v1/identity", "/agent/v1/desired", "/agent/v1/contact":
			if r.TLS == nil || r.TLS.Version < tls.VersionTLS12 {
				http.NotFound(w, r)
				return
			}
			deadline := time.Now().Add(5 * time.Second)
			if err := http.NewResponseController(w).SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
				agentError(w, http.StatusServiceUnavailable, "unavailable")
				return
			}
			if r.URL.RawQuery != "" || r.URL.ForceQuery {
				agentError(w, http.StatusBadRequest, "invalid_request")
				return
			}
			switch r.URL.Path {
			case "/agent/v1/enroll":
				enrollAgent(w, r, authenticator)
			case "/agent/v1/identity":
				identifyAgent(w, r, authenticator)
			case "/agent/v1/desired":
				readAgentDesired(w, r, authenticator, desired)
			case "/agent/v1/contact":
				if contacts == nil {
					http.NotFound(w, r)
					return
				}
				agentContact(w, r, authenticator, contacts)
			}
		default:
			health.ServeHTTP(w, r)
		}
	})
}

func enrollAgent(w http.ResponseWriter, r *http.Request, authenticator AgentAuthenticator) {
	if !allowAgentMethod(w, r, http.MethodPost) {
		return
	}
	invitation, ok := agentBearer(r, agentcredential.Invitation)
	if !ok {
		agentUnauthorized(w)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		agentError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	candidate, ok := readEnrollmentBody(w, r)
	if !ok {
		return
	}
	if _, ok := agentcredential.Parse(candidate, agentcredential.Working); !ok {
		agentUnauthorized(w)
		return
	}
	id, err := authenticator.Enroll(r.Context(), invitation, candidate)
	writeAgentIdentity(w, id, err)
}

func identifyAgent(w http.ResponseWriter, r *http.Request, authenticator AgentAuthenticator) {
	if !allowAgentMethod(w, r, http.MethodGet) {
		return
	}
	if r.ContentLength != 0 {
		agentError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	working, ok := agentBearer(r, agentcredential.Working)
	if !ok {
		agentUnauthorized(w)
		return
	}
	id, err := authenticator.Identity(r.Context(), working)
	writeAgentIdentity(w, id, err)
}

func allowAgentMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	agentError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	return false
}

func agentBearer(r *http.Request, kind agentcredential.Kind) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	_, valid := agentcredential.Parse(token, kind)
	return token, valid
}

func readEnrollmentBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.ContentLength > maxEnrollmentBody {
		agentError(w, http.StatusRequestEntityTooLarge, "request_too_large")
		return "", false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEnrollmentBody))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		agentError(w, http.StatusRequestEntityTooLarge, "request_too_large")
		return "", false
	}
	var request struct {
		Credential string `json:"credential"`
	}
	if err != nil || jsonv2.Unmarshal(body, &request, jsonv2.RejectUnknownMembers(true)) != nil || request.Credential == "" {
		agentError(w, http.StatusBadRequest, "invalid_request")
		return "", false
	}
	return request.Credential, true
}

func writeAgentIdentity(w http.ResponseWriter, id agentcredentials.Identity, err error) {
	if errors.Is(err, agentcredentials.ErrUnauthorized) {
		agentUnauthorized(w)
		return
	}
	if err != nil || id.RouterID == "" || id.Generation < 1 {
		agentError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeAgentJSON(w, http.StatusOK, struct {
		RouterID string `json:"routerId"`
	}{id.RouterID})
}

func agentUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	agentError(w, http.StatusUnauthorized, "unauthorized")
}

func agentError(w http.ResponseWriter, status int, code string) {
	writeAgentJSON(w, status, struct {
		Code string `json:"code"`
	}{code})
}

func writeAgentJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
