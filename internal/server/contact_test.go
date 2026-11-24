package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

type contactStub struct{ called int }

func (s *contactStub) Accept(_ context.Context, id agentcredentials.Identity, report *reporting.Report) error {
	s.called++
	if id.RouterID != "router-example" || id.Generation != 7 {
		return reporting.ErrUnavailable
	}
	if report != nil && report.Sequence == 9 {
		return reporting.ErrConflict
	}
	return nil
}
func (*contactStub) Cursor(context.Context, agentcredentials.Identity) (int64, error) { return 9, nil }
func TestContactWire(t *testing.T) {
	key := agentcredential.Generate(agentcredential.Working).Encode()
	auth := &stubAgentAuthenticator{t: t, key: key}
	port := &contactStub{}
	handler := NewAgentHandler(auth, nil, port)
	for _, sample := range []struct {
		method, body string
		status       int
	}{
		{"GET", "", 200}, {"POST", "", 204},
		{"POST", `{"report":{"sequence":"9","revision":"1","state":"applied"}}`, 409},
		{"POST", `{"report":null}`, 400},
		{"POST", `{"report":{"sequence":"1","revision":"1","state":"applied","appliedRevision":null}}`, 400},
		{"POST", `{"report":{"sequence":"09","revision":"1","state":"applied"}}`, 400},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, agentRequest(sample.method, "/agent/v1/contact", key, sample.body))
		if response.Code != sample.status || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("response: %d %s", response.Code, response.Body)
		}
		if sample.status == 200 && strings.TrimSpace(response.Body.String()) != `{"sequence":"9"}` {
			t.Fatal(response.Body.String())
		}
	}
	if port.called != 2 {
		t.Fatalf("contact writes: %d", port.called)
	}
}
