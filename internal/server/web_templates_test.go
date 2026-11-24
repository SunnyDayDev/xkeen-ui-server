package server

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type stubWebTemplateList struct {
	rows             []webadmin.TemplateSummary
	err              error
	token            string
	createErr        error
	createdID, proof string
	createCalls      int
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
		{"unsupported method", "https://admin.example/ui/v1/templates", "PUT", `{"state":"method_not_allowed"}`, 405, nil, nil, true},
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

func (s *stubWebTemplateList) Create(_ context.Context, token, proof, id string) (webadmin.TemplateSummary, error) {
	s.createCalls++
	s.token, s.proof, s.createdID = token, proof, id
	return webadmin.TemplateSummary{ID: id, Version: 1}, s.createErr
}

func TestWebTemplateCreateHTTPContract(t *testing.T) {
	origin, err := ParseWebOrigin("https://admin.example")
	if err != nil {
		t.Fatal(err)
	}
	store := &stubWebTemplateList{}
	handler := NewWebTemplatesHandler(origin, store, http.NotFoundHandler())
	const body = `{"id":" branch / # "}`
	for _, row := range []struct {
		name, raw, media, origin, state string
		proof                           []string
		err                             error
		status                          int
		reaches                         bool
	}{
		{"created", body, "application/json", "https://admin.example", "", []string{"csrf"}, nil, 201, true},
		{"duplicate", body, "application/json", "https://admin.example", "template_exists", []string{"csrf"}, templates.ErrConflict, 409, true},
		{"inactive", body, "application/json", "https://admin.example", "signed_out", []string{"csrf"}, webadmin.ErrSignedOut, 401, true},
		{"forged-proof", body, "application/json", "https://admin.example", "invalid_csrf", []string{"bad"}, webadmin.ErrCSRF, 403, true},
		{"missing-proof", body, "application/json", "https://admin.example", "invalid_csrf", nil, webadmin.ErrCSRF, 403, true},
		{"duplicate-proof", body, "application/json", "https://admin.example", "invalid_csrf", []string{"csrf", "other"}, webadmin.ErrCSRF, 403, true},
		{"foreign-origin", body, "application/json", "https://foreign.example", "invalid_origin", []string{"csrf"}, nil, 403, false},
		{"missing-origin", body, "application/json", "", "invalid_origin", []string{"csrf"}, nil, 403, false},
		{"unknown-member", `{"id":"example","name":"example"}`, "application/json", "https://admin.example", "invalid_request", []string{"csrf"}, nil, 400, false},
		{"repeated-member", `{"id":"example","id":"other"}`, "application/json", "https://admin.example", "invalid_request", []string{"csrf"}, nil, 400, false},
		{"wrong-case", `{"ID":"example"}`, "application/json", "https://admin.example", "invalid_request", []string{"csrf"}, nil, 400, false},
		{"malformed-unicode", `{"id":"\ud800"}`, "application/json", "https://admin.example", "invalid_request", []string{"csrf"}, nil, 400, false},
		{"empty", `{"id":""}`, "application/json", "https://admin.example", "invalid_request", []string{"csrf"}, nil, 400, false},
		{"null", `{"id":null}`, "application/json", "https://admin.example", "invalid_request", []string{"csrf"}, nil, 400, false},
		{"trailing", body + `{}`, "application/json", "https://admin.example", "invalid_request", []string{"csrf"}, nil, 400, false},
		{"media", body, "text/plain", "https://admin.example", "unsupported_media_type", []string{"csrf"}, nil, 415, false},
		{"size", `{"id":"` + strings.Repeat("x", 8192) + `"}`, "application/json", "https://admin.example", "request_too_large", []string{"csrf"}, nil, 413, false},
		{"unavailable", body, "application/json", "https://admin.example", "unavailable", []string{"csrf"}, errors.New("private marker"), 503, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			*store = stubWebTemplateList{createErr: row.err}
			req := httptest.NewRequest(http.MethodPost, "https://admin.example/ui/v1/templates", strings.NewReader(row.raw))
			req.Header.Set("Content-Type", row.media)
			if row.origin != "" {
				req.Header.Set("Origin", row.origin)
			}
			for _, proof := range row.proof {
				req.Header.Add("X-CSRF-Token", proof)
			}
			req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "synthetic"})
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			var reply struct{ ID, Version, State string }
			err := json.Unmarshal(recorder.Body.Bytes(), &reply)
			if err != nil || row.status != recorder.Code || row.state != reply.State || recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("reply %d %q %v", recorder.Code, recorder.Body.String(), err)
			}
			if row.reaches != (store.createCalls == 1) || (row.reaches && (store.token != "synthetic" || store.createdID != " branch / # ")) {
				t.Fatal("unexpected application call")
			}
			if len(row.proof) != 1 && store.proof != "" {
				t.Fatal("passed ambiguous CSRF proof")
			}
			if row.status == 201 && (reply.ID != " branch / # " || reply.Version != "1") {
				t.Fatalf("changed ID or version: %#v", reply)
			}
			if strings.Contains(recorder.Body.String(), "private marker") {
				t.Fatal("reflection of internal error")
			}
		})
	}
}
