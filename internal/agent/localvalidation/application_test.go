package localvalidation

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/restart"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
)

type syntheticApply func(context.Context, string, string) (restart.Confirmed, error)

func (f syntheticApply) Apply(ctx context.Context, key, tag string) (restart.Confirmed, error) {
	return f(ctx, key, tag)
}

func TestApplicationHandlerPassesOnlyBoundCredentialAndTag(t *testing.T) {
	scope := preparation.Scope{Origin: "https://controller.example", RouterID: "router-example", Profile: "26.3.27", VerifierID: "fixture"}
	key := agentcredential.Generate(agentcredential.Working).Encode()
	input := applicationRequest{Binding: binding{Role: applicationRole, Action: "apply", Scope: scope,
		Tag: `"v1-` + strings.Repeat("a", 64) + `"`, Nonce: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, Credential: key}
	calls := 0
	handler := ApplicationHandler(scope, syntheticApply(func(_ context.Context, got, tag string) (restart.Confirmed, error) {
		calls++
		if got != key || tag != input.Binding.Tag {
			t.Fatal("application call was not bound to the request")
		}
		return restart.Confirmed{}, nil
	}))
	send := func(req applicationRequest) *httptest.ResponseRecorder {
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, applicationEndpoint, bytes.NewReader(data))
		request.Header.Set("Content-Type", "application/json")
		reply := httptest.NewRecorder()
		handler.ServeHTTP(reply, request)
		return reply
	}
	if reply := send(input); reply.Code != http.StatusOK || strings.Contains(reply.Body.String(), key) || calls != 1 {
		t.Fatal("expected an accepted response without credential contents")
	}
	input.Binding.Scope.RouterID = "elsewhere-example"
	if reply := send(input); reply.Code != http.StatusBadRequest || calls != 1 {
		t.Fatal("a different scope reached the application")
	}
}
