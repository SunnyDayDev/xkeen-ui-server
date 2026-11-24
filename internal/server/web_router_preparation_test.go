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
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type stubRouterPreparation struct {
	id, proof string
	input     webadmin.PreparationConfirmation
	err       error
}

func (s *stubRouterPreparation) Describe(_ context.Context, _, id string) (webadmin.PreparationForm, error) {
	s.id = id
	return webadmin.PreparationForm{
		RouterID: "router-a", TemplateID: id, Kind: webadmin.PreparationAccepted, Version: math.MaxInt64,
		Elements: []webadmin.PreparationElement{{Section: "outbounds", ElementID: "element-a", Variables: []webadmin.PreparationVariable{{Name: "value", Type: "number", Required: true}}}},
	}, s.err
}
func (s *stubRouterPreparation) Confirm(_ context.Context, _, proof string, in webadmin.PreparationConfirmation) (webadmin.PreparedRouter, error) {
	s.proof, s.input = proof, in
	return webadmin.PreparedRouter{RouterID: in.RouterID, Name: in.Name, TemplateID: in.TemplateID, Revision: 1}, s.err
}
func (s *stubRouterPreparation) Prepared(_ context.Context, _, id string) (webadmin.PreparedRouter, error) {
	s.id = id
	return webadmin.PreparedRouter{RouterID: id, Revision: 1}, s.err
}
func TestWebRouterPreparationWire(t *testing.T) {
	origin, err := ParseWebOrigin("https://admin.example")
	if err != nil {
		t.Fatal(err)
	}
	service := &stubRouterPreparation{}
	handler := NewWebRouterPreparationHandler(context.Background(), origin, service, NewWebShellHandler(origin, http.NotFoundHandler()))
	const path = "/ui/v1/routers/prepare"
	const confirm = `{"routerId":"router-a","templateId":" x / # ","name":"Филиал","source":"accepted","version":"9223372036854775807","values":[{"elementId":"element-a","name":"value","source":"9007199254740993"}]}`
	check := func(method, path, origin, body string, want int) string {
		t.Helper()
		r := httptest.NewRequest(method, "https://admin.example"+path, strings.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", "synthetic-proof")
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: %d %q", method, path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	form := check("GET", path+"?templateId=+x+%2F+%23+", "https://admin.example", "", 200)
	if service.id != " x / # " || !strings.Contains(form, `"version":"9223372036854775807"`) || !strings.Contains(form, `"required":true`) {
		t.Fatalf("inexact source: %q, %q", service.id, form)
	}
	check("GET", path+"?routerId=router-a", "https://admin.example", "", 200)
	if service.id != "router-a" {
		t.Fatal(service.id)
	}
	check("GET", path+"?routerId=x&templateId=y", "https://admin.example", "", 400)
	check("POST", path, "https://foreign.example", confirm, 403)
	check("POST", path+"?", "https://admin.example", confirm, 400)
	check("POST", path, "https://admin.example", strings.Replace(confirm, `"9223372036854775807"`, `"+1"`, 1), 400)
	check("POST", path, "https://admin.example", confirm, 201)
	if service.proof != "synthetic-proof" || service.input.Version != math.MaxInt64 || service.input.Values[0].Source != "9007199254740993" {
		t.Fatalf("changed confirmation %#v", service.input)
	}
	for _, row := range []struct {
		err    error
		status int
		state  string
	}{
		{webadmin.ErrSignedOut, 401, "signed_out"}, {webadmin.ErrCSRF, 403, "invalid_csrf"},
		{publication.ErrConflict, 409, "conflict"}, {publication.ErrCatalogRejected, 422, "catalog_rejected"},
		{publication.ErrValidatorUnavailable, 503, "validator_unavailable"}, {errors.New("private marker"), 503, "unavailable"},
		{&webadmin.PreparationInputError{Code: "missing_value", ElementID: "element-a", Variable: "value"}, 422, "invalid_input"},
	} {
		service.err = row.err
		var body struct{ State string }
		reply := check("POST", path, "https://admin.example", confirm, row.status)
		if json.Unmarshal([]byte(reply), &body) != nil || body.State != row.state || strings.Contains(reply, "private marker") {
			t.Fatalf("unsafe/incorrect diagnostic %q", reply)
		}
	}
	check("GET", "/ui/routers/connect", "https://admin.example", "", 200)
}

type shutdownRouterPreparation struct {
	stubRouterPreparation
	entered chan struct{}
}

func (s shutdownRouterPreparation) Confirm(ctx context.Context, _ string, _ string, _ webadmin.PreparationConfirmation) (webadmin.PreparedRouter, error) {
	close(s.entered)
	<-ctx.Done()
	return webadmin.PreparedRouter{}, ctx.Err()
}

func TestWebRouterPreparationStopsValidationOnShutdown(t *testing.T) {
	lifetime, stop := context.WithCancel(t.Context())
	defer stop()
	origin, err := ParseWebOrigin("https://admin.example")
	if err != nil {
		t.Fatal(err)
	}
	worker := shutdownRouterPreparation{entered: make(chan struct{})}
	handler := NewWebRouterPreparationHandler(lifetime, origin, &worker, http.NotFoundHandler())
	req := httptest.NewRequest(http.MethodPost, "https://admin.example/ui/v1/routers/prepare",
		strings.NewReader(`{"routerId":"router-a","templateId":"template-a","name":"Example","source":"draft","version":"1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://admin.example")
	done := make(chan struct{})
	go func() { handler.ServeHTTP(httptest.NewRecorder(), req); close(done) }()
	select {
	case <-worker.entered:
	case <-time.After(time.Second):
		t.Fatal("validation was not invoked")
	}
	stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown left validation active")
	}
}
