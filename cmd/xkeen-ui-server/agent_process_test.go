package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func TestProcessAgentTLSInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	cert, _, _ := fixtureServerCertificate(t)
	invalidKey := filepath.Join(dir, "synthetic-private-path.pem")
	diagnosticMarker := "sensitive-test-marker"
	if err := os.WriteFile(invalidKey, []byte(diagnosticMarker), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--tls-cert", cert}, {"--tls-cert", cert, "--tls-key", invalidKey}, {"--tls-cert", "", "--tls-key", ""}} {
		_, stderr, code := runBinaryInDir(t, dir, args...)
		if code != 2 || !strings.Contains(stderr, "TLS") || strings.Contains(stderr, "listening") ||
			strings.Contains(stderr, "synthetic-private-path") || strings.Contains(stderr, diagnosticMarker) {
			t.Fatalf("unsafe TLS config result code=%d, stderr=%q", code, stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, "state")); !os.IsNotExist(err) {
			t.Fatalf("invalid TLS config touched storage: %v", err)
		}
	}
}

func TestProcessHTTPDoesNotEnableAgentEndpoints(t *testing.T) {
	p := startProcess(t, testBinary, "--listen", "127.0.0.1:0")
	address := p.ready(t)
	client := &http.Client{Timeout: time.Second}
	req, err := http.NewRequest(http.MethodGet, "http://"+address+"/agent/v1/identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Forwarded-Proto", "https")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("plaintext agent endpoint status = %d", response.StatusCode)
	}
	stopAgentProcess(t, p)
}

func TestProcessAgentTLSAndSQLiteEnrollment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.sqlite")
	invitation, credentials := fixtureEnrollment(t, path)
	cert, key, trust := fixtureServerCertificate(t)
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: trust}}}
	defer client.CloseIdleConnections()

	launch := func() (*process, string) {
		p := startProcess(t, testBinary, "--listen", "127.0.0.1:0", "--db-path", path, "--tls-cert", cert, "--tls-key", key)
		return p, "https://" + p.readyWithClient(t, client, "https")
	}
	p, origin := launch()
	for _, config := range []*tls.Config{{RootCAs: x509.NewCertPool()}, {RootCAs: trust, ServerName: "wrong.example"}} {
		scoped := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: config}}
		response, err := scoped.Get(origin + "/healthz")
		if err == nil {
			response.Body.Close()
			t.Fatal("unexpected success for untrusted TLS")
		}
		scoped.CloseIdleConnections()
	}
	good := agentcredential.Generate(agentcredential.Working).Encode()
	other := agentcredential.Generate(agentcredential.Working).Encode()
	enroll := func(origin, candidate string) (int, map[string]string) {
		return requestAgentJSON(t, client, "POST", origin+"/agent/v1/enroll", invitation, `{"credential":"`+candidate+`"}`)
	}
	identify := func(origin, candidate string) (int, map[string]string) {
		return requestAgentJSON(t, client, "GET", origin+"/agent/v1/identity", candidate, "")
	}
	if status, body := enroll(origin, good); status != 200 || len(body) != 1 || body["routerId"] != "router-example" {
		t.Fatalf("enrollment = %d, %v", status, body)
	}
	stopAgentProcess(t, p)
	p, origin = launch()
	if status, body := enroll(origin, good); status != 200 || body["routerId"] != "router-example" {
		t.Fatalf("reopened exact replay = %d, %v", status, body)
	}
	if status, body := enroll(origin, other); status != 401 || body["code"] != "unauthorized" {
		t.Fatalf("conflicting replay = %d, %v", status, body)
	}
	if status, body := identify(origin, good); status != 200 || body["routerId"] != "router-example" {
		t.Fatalf("active identity = %d, %v", status, body)
	}
	if err := credentials.Revoke(t.Context(), "router-example"); err != nil {
		t.Fatal(err)
	}
	if status, body := identify(origin, good); status != 401 || body["code"] != "unauthorized" {
		t.Fatalf("revoked identity = %d, %v", status, body)
	}
	stopAgentProcess(t, p)
	if strings.Contains(p.output(), good) || strings.Contains(p.output(), invitation) {
		t.Fatal("credential was printed by the server")
	}
}

func fixtureEnrollment(t *testing.T, path string) (string, *agentcredentials.Service) {
	t.Helper()
	ctx := t.Context()
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlite.NewTemplateRepository(db).Insert(ctx, configuration.TemplateDraft{ID: "template-example"}); err != nil {
		t.Fatal(err)
	}
	version := int64(1)
	if _, err := sqlite.NewPublications(db).Commit(ctx, publication.CommitPlan{
		FirstConfirmation: true, AcceptInitialTemplate: true,
		Expected:    publication.Expectations{RouterID: "router-example", TemplateID: "template-example", TemplateDraftVersion: &version},
		Settings:    routersettings.Draft{ID: "router-example", TemplateID: "template-example"},
		NewRevision: fixtureDesiredCatalog("{}\n"),
	}); err != nil {
		t.Fatal(err)
	}
	credentials := agentcredentials.NewService(sqlite.NewAgentCredentials(db), time.Now)
	invitation, err := credentials.Issue(ctx, "router-example", agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	return invitation, credentials
}

func fixtureServerCertificate(t *testing.T) (string, string, *x509.CertPool) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	trust := x509.NewCertPool()
	if !trust.AppendCertsFromPEM(certPEM) {
		t.Fatal("could not load synthetic certificate")
	}
	return certPath, keyPath, trust
}

func requestAgentJSON(t *testing.T, client *http.Client, method, url, token, body string) (int, map[string]string) {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var value map[string]string
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, value
}

func stopAgentProcess(t *testing.T, p *process) {
	t.Helper()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := p.wait(t); err != nil {
		t.Fatalf("agent server shutdown: %v", err)
	}
}

func TestProcessScopedEnrollmentPreservesAnotherRoutersInvitation(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "server.sqlite")
	invitation, credentials := fixtureEnrollment(t, path)
	cert, key, roots := fixtureServerCertificate(t)
	process := startProcess(t, testBinary, "--db-path", path, "--listen", "127.0.0.1:0", "--tls-cert", cert, "--tls-key", key)
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	defer client.CloseIdleConnections()
	endpoint := "https://" + process.readyWithClient(t, client, "https") + "/agent/v1/enroll"
	state := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, statefile.InvitationName), []byte(invitation), 0600); err != nil {
		t.Fatal(err)
	}
	agent := startProcess(t, buildAgentForProcessTest(t), "--server", strings.TrimSuffix(endpoint, "/agent/v1/enroll"),
		"--state-dir", state, "--ca-file", cert, "--expected-router-id", "different.example")
	if err := agent.wait(t); err == nil || !strings.Contains(agent.output(), "agent credential was rejected") {
		t.Fatal("wrong-scope agent did not refuse enrollment")
	}
	pending := readAgentState(t, state)
	if pending.RouterID != "" || pending.InvitationDigest == "" {
		t.Fatal("mismatched agent persisted an active identity")
	}
	if _, err := credentials.Identity(ctx, pending.Key); !errors.Is(err, agentcredentials.ErrUnauthorized) {
		t.Fatal("wrong-scope CLI enrolled a key")
	}
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if status, err := sqlite.NewContacts(db, time.Now).Status(ctx, "router-example"); err != nil || status.Presence != reporting.Unobserved {
		t.Fatal("wrong-scope agent caused a server contact")
	}

	candidate := agentcredential.Generate(agentcredential.Working).Encode()
	request := func(router string, want int) {
		t.Helper()
		body := fmt.Sprintf(`{"credential":%q,"routerId":%q}`, candidate, router)
		if status, _ := requestAgentJSON(t, client, http.MethodPost, endpoint, invitation, body); status != want {
			t.Fatalf("bound enrollment: received %d, want %d", status, want)
		}
	}
	request("different.example", 401)
	if _, err := credentials.Identity(ctx, candidate); !errors.Is(err, agentcredentials.ErrUnauthorized) {
		t.Fatal("wrong-scope attempt registered a working key")
	}
	request("router-example", 200)
	request("different.example", 401)
	request("router-example", 200)
	if id, err := credentials.Identity(ctx, candidate); err != nil || id.RouterID != "router-example" || id.Generation != 1 {
		t.Fatal("the intended router could not redeem or replay its invitation")
	}
}
