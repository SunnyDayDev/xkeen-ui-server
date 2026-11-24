package server

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
)

type fixtureAccess struct {
	changed int
	enabled bool
}

func (f *fixtureAccess) Admit(context.Context, []string) (externalaccess.Permit, error) {
	if !f.enabled {
		return externalaccess.Permit{}, externalaccess.ErrDisabled
	}
	return externalaccess.Permit{Version: 1}, nil
}
func (f *fixtureAccess) Owner(_ context.Context, _ [32]byte, c *externalaccess.Change) (externalaccess.Settings, string, error) {
	if c != nil {
		f.changed++
	}
	return externalaccess.Settings{Version: 1}, "", nil
}
func TestExternalOriginAndOwnerScope(t *testing.T) {
	f := &fixtureAccess{enabled: true}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) })
	h := NewExternalAccessHandler(WebOrigin("https://console.example"), f, func(w http.ResponseWriter, r *http.Request, p externalaccess.Permit) { w.WriteHeader(204) }, next)
	for _, tc := range []struct {
		path, method, origin string
		tls                  bool
		status               int
	}{
		{"/api/v1/templates/item", "PATCH", "", true, 204}, {"/api/v1/templates/item", "PATCH", "https://other.example", true, 403}, {"/api/v1/templates/item", "PATCH", "null", true, 403}, {"/api/v1/templates/item", "PATCH", "https://console.example:443", true, 204}, {"/api/v1/templates/item", "GET", "", false, 404}, {"/ui/v1/settings/external-api", "PATCH", "", true, 403}, {"/ui/v1/settings/external-api/token", "POST", "https://other.example", true, 403}, {"/ui/v1/settings/external-api", "GET", "", true, 401}, {"/agent/v1/desired", "GET", "", false, 202}} {
		r := httptest.NewRequest(tc.method, "https://console.example"+tc.path, strings.NewReader("{}"))
		if tc.tls {
			r.TLS = &tls.ConnectionState{Version: tls.VersionTLS13}
		} else {
			r.TLS = nil
		}
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%s %s got %d want %d", tc.method, tc.path, w.Code, tc.status)
		}
	}
	if f.changed != 0 {
		t.Fatal("owner mutation without authority")
	}
}

func TestExternalFailureUsesDocumentedErrorEnvelope(t *testing.T) {
	w := httptest.NewRecorder()
	externalFailure(w, externalaccess.ErrUnauthorized)
	if !strings.Contains(w.Body.String(), `"code":"unauthorized"`) || w.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatal("management error envelope")
	}
}
