package server

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
)

type stubAgentAuthenticator struct {
	t              *testing.T
	invite, key    string
	enroll, verify int
	err            error
}

func (s *stubAgentAuthenticator) EnrollForRouter(_ context.Context, invite, key, _ string) (agentcredentials.Identity, error) {
	s.enroll++
	if invite != s.invite || key != s.key {
		s.t.Fatal("incorrect credentials passed to enrollment")
	}
	return agentcredentials.Identity{RouterID: "router-example", Generation: 7}, s.err
}

func (s *stubAgentAuthenticator) Identity(_ context.Context, key string) (agentcredentials.Identity, error) {
	s.verify++
	if key != s.key {
		s.t.Fatal("incorrect working key passed to identity")
	}
	return agentcredentials.Identity{RouterID: "router-example", Generation: 7}, s.err
}

func TestAgentHandlerEnrollmentAndIdentity(t *testing.T) {
	stub := &stubAgentAuthenticator{
		t: t, invite: agentcredential.Generate(agentcredential.Invitation).Encode(),
		key: agentcredential.Generate(agentcredential.Working).Encode(),
	}
	handler := NewAgentHandler(stub, nil, nil)
	tests := []struct {
		method, path, token, body string
	}{
		{http.MethodPost, "/agent/v1/enroll", stub.invite, `{"credential":"` + stub.key + `"}`},
		{http.MethodGet, "/agent/v1/identity", stub.key, ""},
	}
	for _, test := range tests {
		request := agentRequest(test.method, test.path, test.token, test.body)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 200 || strings.TrimSpace(response.Body.String()) != `{"routerId":"router-example"}` {
			t.Fatalf("%s = %d %q", test.path, response.Code, response.Body)
		}
		if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("headers=%v", response.Header())
		}
	}
	if stub.enroll != 1 || stub.verify != 1 {
		t.Fatalf("operations = %d enroll and %d identity", stub.enroll, stub.verify)
	}
}

func TestAgentHandlerFailsClosedOnMalformedInput(t *testing.T) {
	stub := &stubAgentAuthenticator{
		t: t, invite: agentcredential.Generate(agentcredential.Invitation).Encode(),
		key: agentcredential.Generate(agentcredential.Working).Encode(),
	}
	handler := NewAgentHandler(stub, nil, nil)
	validBody := `{"credential":"` + stub.key + `"}`
	tests := []struct {
		name, method, path, token, body string
		edit                            func(*http.Request)
		want                            int
	}{
		{"forwarded plaintext", "GET", "/agent/v1/identity", stub.key, "", func(req *http.Request) {
			req.TLS = nil
			req.Header.Set("X-Forwarded-Proto", "https")
		}, 404},
		{"wrong method", "POST", "/agent/v1/identity", stub.key, "", nil, 405},
		{"missing key", "GET", "/agent/v1/identity", "", "", nil, 401},
		{"invitation is not working key", "GET", "/agent/v1/identity", stub.invite, "", nil, 401},
		{"duplicate header", "GET", "/agent/v1/identity", stub.key, "", func(req *http.Request) {
			req.Header.Add("Authorization", "Bearer "+stub.key)
		}, 401},
		{"foreign query", "GET", "/agent/v1/identity?routerId=other", stub.key, "", nil, 400},
		{"unexpected identity body", "GET", "/agent/v1/identity", stub.key, `{"routerId":"other"}`, nil, 400},
		{"duplicate JSON member", "POST", "/agent/v1/enroll", stub.invite, `{"credential":"a",` + validBody[1:], nil, 400},
		{"case-alias JSON member", "POST", "/agent/v1/enroll", stub.invite, `{"Credential":"` + stub.key + `"}`, nil, 400},
		{"foreign body member", "POST", "/agent/v1/enroll", stub.invite, `{"unexpected":"other",` + validBody[1:], nil, 400},
		{"missing JSON member", "POST", "/agent/v1/enroll", stub.invite, "{}", nil, 400},
		{"trailing body", "POST", "/agent/v1/enroll", stub.invite, validBody + "{}", nil, 400},
		{"oversized body", "POST", "/agent/v1/enroll", stub.invite, validBody + strings.Repeat(" ", 1024), nil, 413},
		{"wrong candidate type", "POST", "/agent/v1/enroll", stub.invite, `{"credential":"` + stub.invite + `"}`, nil, 401},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := agentRequest(test.method, test.path, test.token, test.body)
			if test.edit != nil {
				test.edit(request)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want || strings.Contains(response.Body.String(), "router-example") ||
				strings.Contains(response.Body.String(), stub.invite) || strings.Contains(response.Body.String(), stub.key) {
				t.Fatalf("response = %d %q", response.Code, response.Body)
			}
		})
	}
	if stub.enroll != 0 || stub.verify != 0 {
		t.Fatalf("unexpected service use: %d enrollment and %d identity", stub.enroll, stub.verify)
	}
}

func TestAgentHandlerNormalizesApplicationErrors(t *testing.T) {
	stub := &stubAgentAuthenticator{
		t: t, key: agentcredential.Generate(agentcredential.Working).Encode(),
	}
	handler := NewAgentHandler(stub, nil, nil)
	for _, test := range []struct {
		err  error
		code int
		body string
	}{
		{agentcredentials.ErrUnauthorized, 401, `{"code":"unauthorized"}`},
		{errors.New("private upstream error"), 503, `{"code":"unavailable"}`},
	} {
		stub.err = test.err
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, agentRequest("GET", "/agent/v1/identity", stub.key, ""))
		if response.Code != test.code || strings.TrimSpace(response.Body.String()) != test.body ||
			response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("response = %d %q %v", response.Code, response.Body, response.Header())
		}
	}
}

func agentRequest(method, path, token, body string) *http.Request {
	request := httptest.NewRequest(method, "https://example.net"+path, strings.NewReader(body))
	request.TLS = &tls.ConnectionState{Version: tls.VersionTLS13}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *deadlineRecorder) SetReadDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestAgentHandlerBoundsReadsBeforeCredentialCheck(t *testing.T) {
	handler := NewAgentHandler(&stubAgentAuthenticator{t: t}, nil, nil)
	request := agentRequest(http.MethodPost, "/agent/v1/enroll", "", "{}")
	response := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || response.deadline.IsZero() {
		t.Fatalf("expected authentication denial with input deadline, got %d %v", response.Code, response.deadline)
	}
}
