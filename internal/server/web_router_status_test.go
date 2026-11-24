package server

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

type stubWebStatus struct {
	err   error
	token string
}

func (s *stubWebStatus) List(_ context.Context, token string) ([]webadmin.RouterStatus, error) {
	s.token = token
	return []webadmin.RouterStatus{{RouterID: " Branch / # ", Name: "Филиал", TemplateID: "A", Contact: reporting.Offline, Desired: math.MaxInt64, Applied: 9007199254740993, Runtime: "unknown"}}, s.err
}

func TestWebRouterStatusWire(t *testing.T) {
	origin, err := ParseWebOrigin("https://admin.example")
	if err != nil {
		t.Fatal(err)
	}
	service := &stubWebStatus{}
	handler := NewWebRouterStatusHandler(origin, service, http.NotFoundHandler())
	check := func(method, path, base string, status int) string {
		t.Helper()
		req := httptest.NewRequest(method, base+path, nil)
		req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "synthetic"})
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != status || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status: %d %q", res.Code, res.Body.String())
		}
		return res.Body.String()
	}
	const path = "/ui/v1/routers"
	const base = "https://admin.example"
	out := check("GET", path, base, 200)
	for _, value := range []string{`"routerId":" Branch / # "`, `"desiredRevision":"9223372036854775807"`, `"appliedRevision":"9007199254740993"`, `"contact":"offline"`, `"runtime":"unknown"`} {
		if !strings.Contains(out, value) {
			t.Fatalf("inexact projection %q", out)
		}
	}
	if service.token != "synthetic" {
		t.Fatal("missing session forwarding")
	}
	check("GET", path+"?", base, 400)
	check("POST", path, base, 405)
	check("GET", path, "http://admin.example", 403)
	service.err = webadmin.ErrSignedOut
	check("GET", path, base, 401)
	service.err = errors.New("private marker")
	if body := check("GET", path, base, 503); strings.Contains(body, "private marker") || strings.Contains(body, "Филиал") {
		t.Fatalf("private failure leak: %q", body)
	}
}
