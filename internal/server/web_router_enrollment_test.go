package server

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type stubWebEnrollment struct {
	command webadmin.EnrollmentCommand
	err     error
}

func (s *stubWebEnrollment) Status(_ context.Context, _, id string) (webadmin.EnrollmentState, error) {
	return webadmin.EnrollmentState{RouterID: id, Name: "Филиал", Phase: "pending", Desired: math.MaxInt64, Epoch: 9, Expires: time.Unix(100, 0)}, s.err
}
func (s *stubWebEnrollment) Issue(_ context.Context, _, _ string, command webadmin.EnrollmentCommand) (webadmin.EnrollmentReceipt, error) {
	s.command = command
	return webadmin.EnrollmentReceipt{Epoch: command.Epoch + 1, Expires: time.Unix(100, 0), Token: "synthetic-invitation"}, s.err
}

func TestWebRouterEnrollmentWire(t *testing.T) {
	origin, err := ParseWebOrigin("https://admin.example")
	if err != nil {
		t.Fatal(err)
	}
	service := &stubWebEnrollment{}
	handler := NewWebRouterEnrollmentHandler(origin, "https://agent.example:8443", service, preparationAdmission{}, NewWebShellHandler(origin, http.NotFoundHandler()))
	const path = "/ui/v1/routers/enroll?routerId=branch+%2F+a"
	const body = `{"epoch":"9","desiredRevision":"9223372036854775807","action":"replace"}`
	check := func(target, method, requestOrigin, input string, want int) string {
		t.Helper()
		r := httptest.NewRequest(method, "https://admin.example"+target, strings.NewReader(input))
		if requestOrigin != "" {
			r.Header.Set("Origin", requestOrigin)
		}
		r.Header.Set("X-CSRF-Token", "proof")
		if input != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: %d %q", method, target, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	read := check(path, "GET", "", "", 200)
	if strings.Contains(read, "synthetic-invitation") || !strings.Contains(read, `"origin":"https://agent.example:8443"`) ||
		!strings.Contains(read, `"desiredRevision":"9223372036854775807"`) || !strings.Contains(read, `"routerId":"branch / a"`) {
		t.Fatalf("unsafe or inexact status: %q", read)
	}
	check(path+"&routerId=b", "POST", string(origin), body, 400)
	check(path, "POST", "https://foreign.example", body, 403)
	check(path, "POST", string(origin), strings.Replace(body, `"9"`, `"09"`, 1), 400)
	check(path, "POST", string(origin), strings.Replace(body, `"9"`, `null`, 1), 400)
	check(path, "POST", string(origin), strings.Replace(body, `"9"`, `9`, 1), 400)
	issued := check(path, "POST", string(origin), body, 201)
	if !strings.Contains(issued, "synthetic-invitation") || service.command.RouterID != "branch / a" || !service.command.Replace || service.command.Desired != math.MaxInt64 {
		t.Fatalf("incorrect receipt/command %q %+v", issued, service.command)
	}
	service.err = errors.New("private marker")
	if got := check(path, "POST", string(origin), body, 503); strings.Contains(got, "private marker") || strings.Contains(got, "synthetic-invitation") {
		t.Fatalf("unsafe failure: %q", got)
	}
	check("/ui/routers/enroll?routerId=branch", "GET", "", "", 200)
}
