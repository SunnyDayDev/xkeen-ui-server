package agenthttp_test

import (
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/agenthttp"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
)

func trust(server *httptest.Server) *x509.CertPool {
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	return roots
}

func TestAgentHTTPSUsesTheD03WireContract(t *testing.T) {
	invitation := agentcredential.Generate(agentcredential.Invitation).Encode()
	key := agentcredential.Generate(agentcredential.Working).Encode()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantKey := key
		switch r.URL.Path {
		case "/agent/v1/enroll":
			wantKey = invitation
			body, err := io.ReadAll(r.Body)
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || err != nil ||
				string(body) != `{"credential":"`+key+`"}` {
				t.Error("not an enrollment request")
			}
		case "/agent/v1/identity":
			if r.Method != http.MethodGet || r.ContentLength != 0 {
				t.Error("not an identity request")
			}
		default:
			t.Error("unexpected route")
		}
		if r.Header.Get("Authorization") != "Bearer "+wantKey || len(r.Header.Values("Authorization")) != 1 ||
			r.URL.RawQuery != "" || r.TLS == nil {
			t.Error("invalid credential placement")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, `{"routerId":"router-example"}`)
	}))
	defer server.Close()
	client, err := agenthttp.New(server.URL+"/", trust(server))
	if err != nil || client.Origin() != server.URL {
		t.Fatalf("configured origin: %v", err)
	}
	defer client.CloseIdleConnections()
	if id, err := client.Enroll(t.Context(), invitation, key); id != "router-example" || err != nil {
		t.Fatalf("enrollment: %q, %v", id, err)
	}
	if id, err := client.Identity(t.Context(), key); id != "router-example" || err != nil {
		t.Fatalf("identity: %q, %v", id, err)
	}
}

func TestAgentHTTPSRejectsUntrustedHostsAndRedirects(t *testing.T) {
	var sinkHit atomic.Bool
	sink := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		sinkHit.Store(true)
	}))
	defer sink.Close()
	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, sink.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	pool := trust(server)
	pool.AddCert(sink.Certificate())
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		origin string
		trust  *x509.CertPool
		want   error
	}{
		{server.URL, x509.NewCertPool(), enrollment.ErrTrust},
		{"https://" + net.JoinHostPort("localhost", port), pool, enrollment.ErrTrust},
		{server.URL, pool, enrollment.ErrProtocol},
	} {
		client, err := agenthttp.New(test.origin, test.trust)
		if err != nil {
			t.Fatal(err)
		}
		defer client.CloseIdleConnections()
		key := agentcredential.Generate(agentcredential.Working).Encode()
		if _, err := client.Identity(t.Context(), key); !errors.Is(err, test.want) ||
			strings.Contains(err.Error(), key) {
			t.Fatalf("unsafe TLS/redirect failure: %v", err)
		}
	}
	if requests.Load() != 1 || sinkHit.Load() {
		t.Fatal("authentication was sent to an unverified origin or followed a redirect")
	}
}

func TestAgentHTTPSChecksIdentityAndFailureResponseShapes(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"confirmed rejection", `{"code":"unauthorized"}`, 401, enrollment.ErrUnauthorized},
		{"unknown rejection", `{"code":"private-fixture"}`, 401, enrollment.ErrProtocol},
		{"ambiguous rejection", `{"code":"unauthorized","routerId":"private-fixture"}`, 401, enrollment.ErrProtocol},
		{"temporarily unavailable", `{"code":"unavailable"}`, 503, enrollment.ErrTemporary},
		{"unsupported response", `{"code":"invalid_request"}`, 400, enrollment.ErrProtocol},
		{"duplicate identity", `{"routerId":"old","routerId":"private-fixture"}`, 200, enrollment.ErrProtocol},
		{"missing identity", `{}`, 200, enrollment.ErrProtocol},
		{"oversized response", strings.Repeat("x", 65537), 200, enrollment.ErrProtocol},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client, err := agenthttp.New(server.URL, trust(server))
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			key := agentcredential.Generate(agentcredential.Working).Encode()
			if id, err := client.Identity(t.Context(), key); id != "" || !errors.Is(err, test.want) ||
				strings.Contains(err.Error(), "private-fixture") {
				t.Fatalf("unsafe response result: %v", err)
			}
		})
	}
}

func TestAgentHTTPSRejectsUnsafeOriginBeforeConnecting(t *testing.T) {
	for _, origin := range []string{
		"http://server.example", "https://key@server.example", "https://server.example/path",
		"https://server.example?token=sample", "https://server.example/#", "https://", "https://server.example:65536",
	} {
		if _, err := agenthttp.New(origin, nil); !errors.Is(err, enrollment.ErrOrigin) {
			t.Fatalf("invalid origin result: %v", err)
		}
	}
}
