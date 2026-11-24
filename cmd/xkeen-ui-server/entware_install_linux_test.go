package main

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func startEntwareFixtureTLS(server *httptest.Server, cert, key string) error {
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return err
	}
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	return nil
}

func TestEntwareBootstrapVerifiesReleaseBeforeLaunchingHelper(t *testing.T) {
	script := os.Getenv("XKEEN_TEST_ENTWARE_INSTALLER")
	if script == "" {
		t.Skip("run lab/check-entware-install.sh")
	}
	cert, key, _ := fixtureServerCertificate(t)
	fixtures := map[string]string{
		"xkeen-entware-installer-linux-arm64": "#!/opt/bin/sh\n[ -f \"$2/xkeen-agent-linux-arm64\" ] || exit 78\nprintf 'verified helper invoked\\n'\n",
		"xkeen-agent-linux-arm64":             "fixture executable bytes",
	}
	const version = "v0.0.13"
	var manifest strings.Builder
	for _, name := range []string{"xkeen-entware-installer-linux-arm64", "xkeen-agent-linux-arm64"} {
		fmt.Fprintf(&manifest, "%x  %s\n", sha256.Sum256([]byte(fixtures[name])), name)
	}
	fixtures["SHA256SUMS"] = manifest.String()
	trusted := fmt.Sprintf("%x", sha256.Sum256([]byte(manifest.String())))
	mirror := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutPrefix(r.URL.Path, "/releases/download/"+version+"/")
		if _, exists := fixtures[name]; !ok || !exists {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, fixtures[name])
	}))
	if err := startEntwareFixtureTLS(mirror, cert, key); err != nil {
		t.Fatal(err)
	}
	defer mirror.Close()

	run := func(sum, ver string) ([]byte, error) {
		command := exec.CommandContext(t.Context(), "/opt/bin/sh", script, "install",
			"--role", "agent-transport", "--version", ver, "--manifest-sha256", sum,
			"--run-as", "65532:65532", "--server", "https://controller.example",
			"--release-mirror", mirror.URL, "--mirror-ca-file", cert)
		return command.CombinedOutput()
	}
	for _, request := range []struct{ sum, ver string }{{strings.Repeat("0", 64), version}, {trusted, "v0.0.14"}} {
		output, err := run(request.sum, request.ver)
		if err == nil || strings.Contains(string(output), "verified helper invoked") {
			t.Fatalf("unverified release reached helper: %v, %s", err, output)
		}
	}
	output, err := run(trusted, version)
	if err != nil || !strings.Contains(string(output), "verified helper invoked") {
		t.Fatalf("verified release did not reach helper: %v, %s", err, output)
	}
}

func TestEntwareInstallRegistersAgentAndRestartsSameIdentity(t *testing.T) {
	script := os.Getenv("XKEEN_TEST_ENTWARE_INSTALLER")
	if script == "" {
		t.Skip("run lab/check-entware-install.sh")
	}
	cert, key, roots := fixtureServerCertificate(t)
	dbPath := filepath.Join(t.TempDir(), "server.sqlite")
	invitation, credentials := fixtureEnrollment(t, dbPath)
	inviteFile := filepath.Join(t.TempDir(), "enrollment-invitation")
	if err := os.WriteFile(inviteFile, []byte(invitation+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	origin := "https://" + address
	web := &http.Client{Timeout: time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}}
	defer web.CloseIdleConnections()

	const tag = "v0.0.13"
	mirrorFiles := t.TempDir()
	var manifest strings.Builder
	for _, name := range []string{"xkeen-entware-installer-linux-arm64", "xkeen-agent-linux-arm64"} {
		raw, err := os.ReadFile(filepath.Join("/proof", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(mirrorFiles, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&manifest, "%x  %s\n", sha256.Sum256(raw), name)
	}
	manifestFile := filepath.Join(mirrorFiles, "SHA256SUMS")
	if err := os.WriteFile(manifestFile, []byte(manifest.String()), 0600); err != nil {
		t.Fatal(err)
	}
	mirror := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutPrefix(r.URL.Path, "/releases/download/"+tag+"/")
		switch name {
		case "SHA256SUMS", "xkeen-entware-installer-linux-arm64", "xkeen-agent-linux-arm64":
			if ok {
				http.ServeFile(w, r, filepath.Join(mirrorFiles, name))
				return
			}
		}
		http.NotFound(w, r)
	}))
	if err := startEntwareFixtureTLS(mirror, cert, key); err != nil {
		t.Fatal(err)
	}
	defer mirror.Close()

	const initPath = "/opt/etc/init.d/S92xkeen-agent"
	t.Cleanup(func() { _ = exec.Command(initPath, "stop").Run() })
	install := func(role, chosenOrigin, invitationPath, agentCA string) ([]byte, error) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "/opt/bin/sh", script, "install",
			"--role", role, "--version", tag, "--manifest-sha256",
			fmt.Sprintf("%x", sha256.Sum256([]byte(manifest.String()))), "--run-as", "65532:65532",
			"--server", chosenOrigin, "--ca-file", agentCA, "--invitation-file", invitationPath,
			"--release-mirror", mirror.URL, "--mirror-ca-file", cert)
		return cmd.CombinedOutput()
	}
	if output, err := install("agent-application", origin, inviteFile, cert); err == nil || !strings.Contains(string(output), "unsupported Entware installation role") {
		t.Fatalf("unproved application role: %v, %s", err, output)
	}
	wrongCA := filepath.Join(t.TempDir(), "invalid-ca")
	if err := os.WriteFile(wrongCA, []byte("malformed fixture CA"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := install("agent-transport", origin, inviteFile, wrongCA); err == nil || !strings.Contains(string(output), "agent CA file is invalid") {
		t.Fatalf("unparseable CA was not rejected: %v, %s", err, output)
	}
	if _, err := os.Lstat("/opt/var/lib/xkeen-ui/agent-transport"); !os.IsNotExist(err) {
		t.Fatal("unparseable CA left persistent agent state")
	}
	if output, err := install("agent-transport", origin, inviteFile, cert); err == nil || strings.Contains(string(output), invitation) {
		t.Fatal("unavailable origin did not produce safe refusal")
	}
	if _, err := os.Lstat(initPath); !os.IsNotExist(err) {
		t.Fatal("unready service left its init registration")
	}
	if _, err := os.Lstat("/opt/libexec/xkeen-ui/agent-transport/current"); !os.IsNotExist(err) {
		t.Fatal("unready service left an active generation")
	}
	if output, err := install("agent-transport", "https://other.example", inviteFile, cert); err == nil || !strings.Contains(string(output), "same release and service profile") {
		t.Fatalf("retry reused state across origins: %v, %s", err, output)
	}
	server := startProcess(t, testBinary, "--db-path", dbPath, "--listen", address,
		"--tls-cert", cert, "--tls-key", key)
	server.readyWithClient(t, web, "https")
	if output, err := install("agent-transport", origin, "/nonexistent", cert); err != nil {
		t.Fatalf("same release retry failed: %v, %s", err, output)
	} else if strings.Contains(string(output), invitation) {
		t.Fatal("installation output contains the invitation")
	}

	invoke := func(action string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), initPath, action)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("init.d %s: %v, %s", action, err, output)
		}
	}
	invoke("status")
	const processReceipt = "/opt/var/lib/xkeen-ui/install/agent-transport.process.json"
	savedReceipt, err := os.ReadFile(processReceipt)
	if err != nil {
		t.Fatal(err)
	}
	foreign := exec.Command("/opt/bin/sleep", "120")
	if err := foreign.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = foreign.Process.Kill(); _ = foreign.Wait() }()
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", foreign.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(stat)[strings.LastIndexByte(string(stat), ')')+1:])
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	wrongReceipt, err := json.Marshal(map[string]any{"Boot": strings.TrimSpace(string(boot)), "PID": foreign.Process.Pid, "Tick": fields[19]})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(processReceipt, wrongReceipt, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), initPath, "stop")
	output, stopErr := cmd.CombinedOutput()
	if err := os.WriteFile(processReceipt, savedReceipt, 0600); err != nil {
		t.Fatal(err)
	}
	if stopErr == nil || !strings.Contains(string(output), "instance ownership") || foreign.Process.Signal(syscall.Signal(0)) != nil {
		t.Fatal("init.d did not refuse a live foreign instance")
	}
	invoke("stop")
	const stateDir = "/opt/var/lib/xkeen-ui/agent-transport"
	readState := func() enrollment.State {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(stateDir, "credentials.json"))
		if err != nil {
			t.Fatal("cannot read installed state")
		}
		var value struct{ State enrollment.State }
		if json.Unmarshal(raw, &value) != nil {
			t.Fatal("invalid installed state")
		}
		return value.State
	}
	first := readState()
	if first.RouterID != "router-example" || first.Origin != origin {
		t.Fatal("installed agent did not register against the pinned origin")
	}
	info, err := os.Stat(stateDir)
	if err != nil || info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != 65532 {
		t.Fatal("persistent state is not private to the configured agent user")
	}
	invoke("start")
	invoke("status")
	invoke("stop")
	if got := readState(); got != first {
		t.Fatal("restart changed the saved working identity")
	}
	identity, err := credentials.Identity(t.Context(), first.Key)
	if err != nil || identity.Generation != 1 || identity.RouterID != first.RouterID {
		t.Fatal("install or init.d restart created another registration")
	}
}
