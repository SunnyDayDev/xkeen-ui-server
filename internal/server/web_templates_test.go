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
)

type stubWebTemplateList struct {
	rows  []webadmin.TemplateSummary
	err   error
	token string
}

func (s *stubWebTemplateList) List(_ context.Context, token string) ([]webadmin.TemplateSummary, error) {
	s.token = token
	return s.rows, s.err
}

func TestWebTemplateListHTTPSContract(t *testing.T) {
	origin, err := ParseWebOrigin("https://admin.example")
	if err != nil {
		t.Fatal(err)
	}
	repository := &stubWebTemplateList{}
	handler := NewWebTemplatesHandler(origin, repository, NewWebShellHandler(origin, http.NotFoundHandler()))
	cases := []struct {
		name, url, method, body string
		expected                int
		rows                    []webadmin.TemplateSummary
		err                     error
		private                 bool
	}{
		{"list", "https://admin.example/ui/v1/templates", "GET", `{"templates":[{"id":" a / #","version":"9223372036854775807"}]}`, 200, []webadmin.TemplateSummary{{ID: " a / #", Version: math.MaxInt64}}, nil, true},
		{"empty", "https://admin.example/ui/v1/templates", "GET", `{"templates":[]}`, 200, nil, nil, true},
		{"revoked", "https://admin.example/ui/v1/templates", "GET", `{"state":"signed_out"}`, 401, nil, webadmin.ErrSignedOut, true},
		{"unavailable", "https://admin.example/ui/v1/templates", "GET", `{"state":"unavailable"}`, 503, nil, errors.New("private internal error"), true},
		{"other host", "https://wrong.example/ui/v1/templates", "GET", `{"state":"invalid_origin"}`, 403, nil, nil, true},
		{"plaintext", "http://admin.example/ui/v1/templates", "GET", `{"state":"invalid_origin"}`, 403, nil, nil, true},
		{"query", "https://admin.example/ui/v1/templates?", "GET", `{"state":"invalid_request"}`, 400, nil, nil, true},
		{"unsupported method", "https://admin.example/ui/v1/templates", "POST", `{"state":"method_not_allowed"}`, 405, nil, nil, true},
		{"deep link", "https://admin.example/ui/templates", "GET", "<!doctype html>", 200, nil, nil, true},
		{"no SPA API fallback", "https://admin.example/ui/v1/templates/extra", "GET", "404", 404, nil, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repository.rows, repository.err, repository.token = tc.rows, tc.err, ""
			r := httptest.NewRequest(tc.method, tc.url, nil)
			r.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "synthetic"})
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.expected || !strings.Contains(w.Body.String(), tc.body) || (tc.private && w.Header().Get("Cache-Control") != "no-store") {
				t.Fatalf("got %d %v %q", w.Code, w.Header(), w.Body.String())
			}
			shouldCall := tc.name == "list" || tc.name == "empty" || tc.name == "revoked" || tc.name == "unavailable"
			if (repository.token == "synthetic") != shouldCall {
				t.Fatalf("store received unexpected token %q", repository.token)
			}
		})
	}
}
