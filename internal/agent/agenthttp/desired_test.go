package agenthttp_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/agenthttp"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/desired"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

func TestDesiredHTTPSUsesBoundConditionalAndRejectsBadTransport(t *testing.T) {
	key := agentcredential.Generate(agentcredential.Working).Encode()
	const tag = `"v1-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"`
	var reply atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/agent/v1/desired" || r.URL.RawQuery != "" ||
			r.Header.Get("Authorization") != "Bearer "+key || r.Header.Get("If-None-Match") != tag ||
			r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("desired read used an unexpected wire request")
		}
		switch reply.Load() {
		case 0:
			w.Header().Set("ETag", tag)
			w.WriteHeader(304)
		case 1:
			w.WriteHeader(204)
		case 2:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", strconv.Itoa(agentrevision.MaxBody+1))
			w.WriteHeader(200)
		case 3:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			fmt.Fprint(w, "{}")
		case 4:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", "10")
			fmt.Fprint(w, "{}")
		case 5:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			fmt.Fprint(w, `{"code":"unauthorized"}`)
		}
	}))
	defer server.Close()
	client, err := agenthttp.New(server.URL, trust(server))
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []struct {
		kind desired.ResultKind
		err  error
	}{
		{kind: desired.NotModified}, {kind: desired.Absent},
		{err: enrollment.ErrProtocol}, {err: enrollment.ErrProtocol},
		{err: enrollment.ErrTemporary}, {err: enrollment.ErrUnauthorized},
	} {
		reply.Store(int32(i))
		got, err := client.ReadDesired(t.Context(), key, tag)
		if got.Kind != want.kind || !errors.Is(err, want.err) {
			t.Fatalf("case %d: desired = %v, %v", i, got.Kind, err)
		}
	}
}
