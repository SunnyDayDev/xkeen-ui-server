package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebOriginStrict(t *testing.T) {
	for _, raw := range []string{"http://admin.example", "https://admin.example/", "https://admin.example?", "https://admin.example#", "https://user@admin.example", "https://admin.example:0443", "https://admin.example:65536"} {
		if _, err := ParseWebOrigin(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	origin, err := ParseWebOrigin("https://ADMIN.example:443")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		url, header, forwarded string
		allow                  bool
	}{
		{"https://admin.example", "", "", true},
		{"https://admin.example:443", "https://admin.example", "", true},
		{"https://admin.example", "https://admin.example:443", "", true},
		{"https://admin.example", "https://admin.example/", "", false},
		{"http://admin.example", "", "https", false},
		{"https://foreign.example", "https://admin.example", "https", false},
		{"https://admin.example", "https://foreign.example", "https", false},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.url+"/ui/v1/session", nil)
		if tc.header != "" {
			r.Header.Set("Origin", tc.header)
		}
		r.Header.Set("X-Forwarded-Proto", tc.forwarded)
		r.Header.Set("X-Forwarded-Host", "admin.example")
		if result := origin.allows(r, false); result != tc.allow {
			t.Fatalf("%+v => %v", tc, result)
		}
	}
}

func TestWebPasswordBodyRejectsUnknownAndAmbiguousValues(t *testing.T) {
	for _, raw := range []string{`{"password": "abcdefghijklmnopqrstuvwxyz", "password":"other"}`, `{"PASSWORD":"secret"}`, `{"password":null}`, `{"password":"x","extra":false}`, `{"password":"x"} {}`, `[{"password":"x"}]`} {
		r := httptest.NewRequest(http.MethodPost, "https://admin.example/ui/v1/login", strings.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		if _, err := webLoginPassword(httptest.NewRecorder(), r); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

type webDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
	err      error
}

func (w *webDeadlineRecorder) SetReadDeadline(deadline time.Time) error {
	w.deadline = deadline
	return w.err
}

func TestWebRoutesBoundReadAndRejectURLQuery(t *testing.T) {
	origin, err := ParseWebOrigin("https://admin.example")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewWebAdminHandler(origin, nil, http.NotFoundHandler())
	for _, suffix := range []string{"?unexpected", "?"} {
		start := time.Now()
		r := httptest.NewRequest(http.MethodGet, "https://admin.example/ui/v1/session"+suffix, nil)
		r.Header.Set("Origin", "https://admin.example")
		w := &webDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		handler.ServeHTTP(w, r)
		if w.deadline.Before(start) || w.deadline.After(start.Add(6*time.Second)) || w.Code != http.StatusBadRequest {
			t.Fatalf("read deadline and query rejection: %v, %d", w.deadline, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "https://admin.example/ui/v1/session", nil)
	w := &webDeadlineRecorder{ResponseRecorder: httptest.NewRecorder(), err: errors.New("synthetic transport error")}
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable deadline: %d", w.Code)
	}
}
