package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/validation"
)

const installedAgentOrigin = "https://agent.example:8443"

func entwareValidatorFixture(t *testing.T) ([]string, *atomic.Int64) {
	t.Helper()
	certPath, keyPath, _ := fixtureServerCertificate(t)
	server, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	clientDER, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	client, err := x509.ParseCertificate(clientDER)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	clientPath, privatePath := filepath.Join(dir, "client.pem"), filepath.Join(dir, "client.key")
	for path, block := range map[string]*pem.Block{clientPath: {Type: "CERTIFICATE", Bytes: clientDER}, privatePath: {Type: "PRIVATE KEY", Bytes: keyDER}} {
		if err := os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
			t.Fatal(err)
		}
	}
	roots := x509.NewCertPool()
	roots.AddCert(client)
	tlsConfig, err := validation.WorkerTLS(server, roots, fmt.Sprintf("%x", sha256.Sum256(clientDER)))
	if err != nil {
		t.Fatal(err)
	}
	calls := new(atomic.Int64)
	worker := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var binding struct{ Attempt, Profile, Digest string }
		if r.Method != http.MethodPost || r.TLS == nil || len(r.TLS.VerifiedChains) == 0 ||
			json.NewDecoder(r.Body).Decode(&binding) != nil || binding.Attempt == "" || binding.Profile == "" || binding.Digest == "" {
			http.Error(w, "invalid synthetic request", 400)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct{ Attempt, Profile, Digest, Outcome string }{binding.Attempt, binding.Profile, binding.Digest, "ok"})
	}))
	worker.Config.ErrorLog = log.New(io.Discard, "", 0)
	worker.TLS = tlsConfig
	worker.StartTLS()
	t.Cleanup(worker.Close)
	return []string{"--agent-origin", installedAgentOrigin, "--validator-origin", worker.URL, "--validator-ca", certPath, "--validator-client-cert", clientPath, "--validator-client-key", privatePath}, calls
}

func exerciseInstalledPreparation(t *testing.T, client *http.Client, origin string, cookie *http.Cookie, csrf string, want int) {
	t.Helper()
	request := func(method, path, raw string, want int) []byte {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, origin+path, strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(cookie)
		if method == http.MethodPost {
			req.Header.Set("Origin", origin)
			req.Header.Set("X-CSRF-Token", csrf)
			req.Header.Set("Content-Type", "application/json")
		}
		reply, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer reply.Body.Close()
		result, err := io.ReadAll(io.LimitReader(reply.Body, 64<<10))
		if err != nil || reply.StatusCode != want {
			t.Fatalf("installed %s %s => %d (%v): %s", method, path, reply.StatusCode, err, result)
		}
		return result
	}
	request(http.MethodPost, "/ui/v1/templates", `{"id":"installed-profile"}`, 201)
	request(http.MethodPost, "/ui/v1/templates/item?templateId=installed-profile",
		`{"version":"1","section":"outbounds","source":"{\"tag\":\"installed-direct\",\"protocol\":\"freedom\"}"}`, 200)
	const prepare = "/ui/v1/routers/prepare"
	raw := request(http.MethodGet, prepare+"?templateId=installed-profile", "", 200)
	var form struct{ RouterID, TemplateID, Source, Version string }
	if json.Unmarshal(raw, &form) != nil || form.RouterID == "" {
		t.Fatal("missing prepared router form")
	}
	payload, err := json.Marshal(map[string]any{"routerId": form.RouterID, "templateId": form.TemplateID,
		"name": "Installed fixture", "source": form.Source, "version": form.Version, "values": []string{}})
	if err != nil {
		t.Fatal(err)
	}
	raw = request(http.MethodPost, prepare, string(payload), want)
	if want == 503 && !strings.Contains(string(raw), "validator_unavailable") {
		t.Fatal("unconfigured server did not refuse publication")
	}
	if want == 201 {
		raw = request(http.MethodGet, "/ui/v1/routers/enroll?routerId="+form.RouterID, "", 200)
		var enrollment struct{ Origin string }
		if json.Unmarshal(raw, &enrollment) != nil || enrollment.Origin != installedAgentOrigin || enrollment.Origin == origin {
			t.Fatal("installed F10 did not use the separately pinned HTTPS agent origin")
		}
	}

}
