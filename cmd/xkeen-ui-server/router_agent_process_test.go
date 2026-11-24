package main

import (
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
)

func TestProcessRouterAgentEnrollRestartAndRecover(t *testing.T) {
	agent := buildAgentForProcessTest(t)
	dbPath := filepath.Join(t.TempDir(), "server.sqlite")
	invitation, credentials := fixtureEnrollment(t, dbPath)
	certPath, keyPath, roots := fixtureServerCertificate(t)
	server := startProcess(t, testBinary, "--db-path", dbPath, "--listen", "127.0.0.1:0",
		"--tls-cert", certPath, "--tls-key", keyPath)
	web := &http.Client{Timeout: time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}}
	defer web.CloseIdleConnections()
	origin := "https://" + server.readyWithClient(t, web, "https")

	dir := filepath.Join(t.TempDir(), "agent")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	invitationPath := filepath.Join(dir, statefile.InvitationName)
	if err := os.WriteFile(invitationPath, []byte(invitation+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	launch := func() *process {
		return startProcess(t, agent, "--server", origin, "--state-dir", dir, "--ca-file", certPath)
	}
	first := launch()
	waitForAgentAuthentication(t, first)
	if store, err := statefile.Open(dir); !errors.Is(err, enrollment.ErrState) {
		if store != nil {
			store.Close()
		}
		t.Fatal("foreground process did not hold the state lock")
	}
	stopRouterAgent(t, first)

	store, err := statefile.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	active, err := store.Load()
	if err != nil || active == nil || active.RouterID != "router-example" ||
		active.Origin != origin || active.Key == "" || active.InvitationDigest != "" {
		t.Fatal("initial binding was not persisted")
	}
	identity, err := credentials.Identity(t.Context(), active.Key)
	if err != nil || identity.RouterID != active.RouterID || identity.Generation != 1 {
		t.Fatal("server did not preserve the initial binding")
	}
	assertNoAgentSecret(t, first, invitation, active.Key)

	// Restore the exact local state from just before receipt of the enrollment
	// response. The actual D03 server has already committed the registration.
	pending := *active
	pending.RouterID = ""
	pending.InvitationDigest = fmt.Sprintf("%x", sha256.Sum256([]byte(invitation)))
	if err := store.Save(pending); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(invitationPath); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		resumed := launch()
		waitForAgentAuthentication(t, resumed)
		stopRouterAgent(t, resumed)
		assertNoAgentSecret(t, resumed, invitation, active.Key)
		got := readAgentState(t, dir)
		if *got != *active {
			t.Fatal("restart changed the stored agent registration")
		}
	}
	identity, err = credentials.Identity(t.Context(), active.Key)
	if err != nil || identity.Generation != 1 || identity.RouterID != active.RouterID {
		t.Fatal("restart added another server registration")
	}

	if err := credentials.Revoke(t.Context(), active.RouterID); err != nil {
		t.Fatal(err)
	}
	revoked := launch()
	if err := revoked.wait(t); err == nil || !strings.Contains(revoked.output(), "agent credential was rejected") {
		t.Fatal("revoked working key did not produce a safe refusal")
	}
	assertNoAgentSecret(t, revoked, invitation, active.Key)
	if *readAgentState(t, dir) != *active {
		t.Fatal("revocation changed saved agent credentials")
	}
}

func TestProcessRouterAgentRejectsArgumentsAndUnsafeState(t *testing.T) {
	agent := buildAgentForProcessTest(t)
	invalid := startProcess(t, agent, "--invalid=private-fixture")
	if err := invalid.wait(t); err == nil || strings.Contains(invalid.output(), "private-fixture") {
		t.Fatal("invalid argument did not produce a safe refusal")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	rejected := startProcess(t, agent, "--server", "https://server.example", "--state-dir", dir)
	if err := rejected.wait(t); err == nil || !strings.Contains(rejected.output(), "agent state is unavailable or invalid") {
		t.Fatal("unsafe local state did not prevent launch")
	}
}

func readAgentState(t *testing.T, dir string) *enrollment.State {
	t.Helper()
	store, err := statefile.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.Load()
	if err != nil || got == nil {
		t.Fatal("missing saved agent state")
	}
	return got
}

func assertNoAgentSecret(t *testing.T, p *process, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(p.output(), secret) || strings.Contains(strings.Join(p.cmd.Args, " "), secret) {
			t.Fatal("agent exposed protected fixture material")
		}
	}
}

func buildAgentForProcessTest(t *testing.T) string {
	t.Helper()
	if binary := os.Getenv("XKEEN_TEST_AGENT_BINARY"); binary != "" {
		return binary
	}
	binary := filepath.Join(t.TempDir(), "xkeen-agent")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin/go"), "build", "-trimpath", "-o", binary, "../xkeen-agent")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build agent test binary: %v\n%s", err, output)
	}
	return binary
}

func waitForAgentAuthentication(t *testing.T, p *process) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			t.Fatal("agent exited before startup authentication")
		default:
		}
		if strings.Contains(p.output(), "agent authenticated") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("agent did not authenticate before timeout")
}

func stopRouterAgent(t *testing.T, p *process) {
	t.Helper()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := p.wait(t); err != nil {
		t.Fatalf("agent did not stop gracefully: %v", err)
	}
}
