package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/server"
)

func TestHealth(t *testing.T) {
	assertHealth(t, server.NewHandler())
}

func TestUnsupportedRequests(t *testing.T) {
	for _, tc := range []struct {
		method string
		path   string
		status int
		allow  string
	}{
		{http.MethodHead, "/healthz", http.StatusMethodNotAllowed, "GET"},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed, "GET"},
		{http.MethodGet, "/", http.StatusNotFound, ""},
		{http.MethodGet, "/healthz/", http.StatusNotFound, ""},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			handler := server.NewHandler()
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			if response.Code != tc.status {
				t.Errorf("status = %d, want %d", response.Code, tc.status)
			}
			if got := response.Header().Get("Allow"); got != tc.allow {
				t.Errorf("Allow = %q, want %q", got, tc.allow)
			}
			assertHealth(t, handler)
		})
	}
}

func assertHealth(t *testing.T, handler http.Handler) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !reflect.DeepEqual(body, map[string]any{"status": "ok"}) {
		t.Errorf("body = %#v, want only status:ok", body)
	}
}
