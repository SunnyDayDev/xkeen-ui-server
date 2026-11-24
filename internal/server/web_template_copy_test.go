package server

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type stubTemplateCopy struct {
	err            error
	source, target string
	version        int64
}

func (s *stubTemplateCopy) Copy(_ context.Context, _, _, source, target string, version int64) (webadmin.TemplateSummary, error) {
	s.source, s.target, s.version = source, target, version
	return webadmin.TemplateSummary{ID: target, Version: 1}, s.err
}

func TestWebTemplateCopyWire(t *testing.T) {
	origin, err := ParseWebOrigin("https://admin.example")
	if err != nil {
		t.Fatal(err)
	}
	service := &stubTemplateCopy{}
	handler := NewWebTemplateCopyHandler(origin, service, preparationAdmission{}, NewWebShellHandler(origin, http.NotFoundHandler()))
	const endpoint = "/ui/v1/templates/copy?templateId=+source+%2F+%23+"
	const form = `{"targetId":" next + id ","version":"9223372036854775807"}`
	check := func(method, path, address, origin, body string, want int) string {
		t.Helper()
		r := httptest.NewRequest(method, address+path, strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status %d value %q", w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	check("GET", "/ui/templates/copy?templateId=source", "https://admin.example", "", "", 200)
	check("POST", endpoint, "https://admin.example", "https://bad.example", form, 403)
	check("POST", endpoint+"&templateId=b", "https://admin.example", string(origin), form, 400)
	check("POST", endpoint, "https://admin.example", string(origin), strings.Replace(form, `"9223372036854775807"`, `1`, 1), 400)
	out := check("POST", endpoint, "https://admin.example", string(origin), form, 201)
	if service.source != " source / # " || service.target != " next + id " || service.version != math.MaxInt64 || !strings.Contains(out, `"version":"1"`) {
		t.Fatalf("inexact %#v %q", service, out)
	}
	service.err = templates.ErrVersionConflict
	if out := check("POST", endpoint, "https://admin.example", string(origin), form, 409); !strings.Contains(out, "source_changed") {
		t.Fatal(out)
	}
}
