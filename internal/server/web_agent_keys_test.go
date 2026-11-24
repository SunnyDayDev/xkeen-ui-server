package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type stubAgentKeyManager struct {
	err     error
	command webadmin.AgentKeyCommand
}

func (s *stubAgentKeyManager) Manage(_ context.Context, _, _ string, command webadmin.AgentKeyCommand) (webadmin.EnrollmentReceipt, error) {
	s.command = command
	return webadmin.EnrollmentReceipt{Epoch: command.Epoch + 1, Expires: time.Unix(1000, 0), Token: "synthetic-private"}, s.err
}

func TestWebAgentKeysWireAuditsCommittedMetadata(t *testing.T) {
	origin, err := ParseWebOrigin("https://admin.example")
	if err != nil {
		t.Fatal(err)
	}
	const path = "/ui/v1/routers/keys?routerId=router"
	const nonce = `{"epoch":"11","desiredRevision":"2","action":"`
	service := &stubAgentKeyManager{}
	var audit []string
	auditor := func(action webadmin.AgentKeyAction, id string) { audit = append(audit, string(action)+"/"+id) }
	check := func(originValue string, action string, want int) string {
		t.Helper()
		handler := NewWebAgentKeysHandler(origin, WebOrigin(originValue), service, preparationAdmission{}, auditor, http.NotFoundHandler())
		request := httptest.NewRequest("POST", string(origin)+path, strings.NewReader(nonce+action+`"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", string(origin))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, request)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status %d %q", w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	check("", "rotate", 503)
	check("https://agent.example", "unknown", 400)
	issued := check("https://agent.example", "rotate", 201)
	if !strings.Contains(issued, "synthetic-private") || service.command.Epoch != 11 || service.command.Desired != 2 {
		t.Fatalf("issue result %q command %+v", issued, service.command)
	}
	revoked := check("", "revoke", 200)
	if strings.Contains(revoked, "synthetic-private") || len(audit) != 2 || audit[0] != "rotate/router" || audit[1] != "revoke/router" {
		t.Fatalf("unsafe result or audit: %q %+v", revoked, audit)
	}
	service.err = errors.New("private marker")
	if body := check("https://agent.example", "recover", 503); strings.Contains(body, "private marker") || len(audit) != 2 {
		t.Fatal("failed request exposed private data")
	}
}
