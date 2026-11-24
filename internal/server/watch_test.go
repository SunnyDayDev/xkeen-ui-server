package server

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/revisionwatch"
)

type watchFunc func(context.Context, agentcredentials.Identity, int64) (bool, error)

func (f watchFunc) Wait(ctx context.Context, id agentcredentials.Identity, after int64) (bool, error) {
	return f(ctx, id, after)
}

func TestAgentWatchWireAndScope(t *testing.T) {
	key := agentcredential.Generate(agentcredential.Working).Encode()
	for _, tc := range []struct {
		path            string
		secure, changed bool
		err             error
		code            int
	}{
		{"/agent/v1/watch?after=4", true, true, nil, 200},
		{"/agent/v1/watch?after=4", true, false, nil, 204},
		{"/agent/v1/watch?after=4", true, false, revisionwatch.ErrBusy, 429},
		{"/agent/v1/watch?after=4", true, false, agentcredentials.ErrUnauthorized, 401},
		{"/agent/v1/watch?after=4", true, false, revisionwatch.ErrUnavailable, 503},
		{"/agent/v1/watch?after=04", true, false, nil, 400},
		{"/agent/v1/watch?after=4&routerId=other", true, false, nil, 400},
		{"/agent/v1/watch?after=4", false, false, nil, 404},
	} {
		stub := &stubAgentAuthenticator{t: t, key: key}
		handler := NewAgentHandler(stub, nil, nil, watchFunc(func(_ context.Context, id agentcredentials.Identity, after int64) (bool, error) {
			if after != 4 || id.RouterID == "" || id.Generation < 1 {
				t.Fatal("incorrect scope")
			}
			return tc.changed, tc.err
		}))
		request := httptest.NewRequest(http.MethodGet, tc.path, nil)
		request.Header.Set("Authorization", "Bearer "+key)
		if tc.secure {
			request.TLS = &tls.ConnectionState{Version: tls.VersionTLS12}
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tc.code {
			t.Fatalf("%s: %d want %d", tc.path, response.Code, tc.code)
		}
		if tc.code == 200 && response.Body.String() != "{\"kind\":\"desired_changed\"}\n" {
			t.Fatal(response.Body.String())
		}
	}
}
