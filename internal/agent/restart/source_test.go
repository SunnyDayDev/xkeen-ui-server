package restart

import (
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/agenthttp"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

func TestCentralSourceVerifiesIndependentIdentityAndSnapshot(t *testing.T) {
	f := newApplyFixture(t)
	tag, body, err := agentrevision.Encode(f.scope.RouterID, f.first.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var identity atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer synthetic" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"code":"unauthorized"}`))
			return
		}
		switch r.URL.Path {
		case "/agent/v1/identity":
			if identity.Load() == 0 {
				_, _ = w.Write([]byte(`{"routerId":"router-example"}`))
			} else {
				_, _ = w.Write([]byte(`{"routerId":"foreign-example"}`))
			}
		case "/agent/v1/desired":
			if r.Header.Get("If-None-Match") != "" {
				t.Error("initial read used a conditional request")
			}
			w.Header().Set("ETag", tag)
			_, _ = w.Write(body)
		default:
			t.Error("unexpected route")
		}
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	f.scope.Origin = server.URL
	client, err := agenthttp.New(server.URL, roots)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	source := NewCentralSource(f.scope, client)
	if got, err := source.ReadCurrent(t.Context(), "synthetic"); err != nil || got.Tag != tag {
		t.Fatal("could not read trusted desired revision", err)
	}
	identity.Store(1)
	if _, err := source.ReadCurrent(t.Context(), "synthetic"); err == nil {
		t.Fatal("accepted a different router")
	}
	if _, err := source.ReadCurrent(t.Context(), "revoked"); err == nil {
		t.Fatal("accepted a revoked credential")
	}
	identity.Store(0)
	untrusted, err := agenthttp.New(server.URL, x509.NewCertPool())
	if err != nil {
		t.Fatal(err)
	}
	defer untrusted.CloseIdleConnections()
	if _, err := NewCentralSource(f.scope, untrusted).ReadCurrent(t.Context(), "synthetic"); err == nil {
		t.Fatal("accepted a server with an unrelated certificate")
	}
}
