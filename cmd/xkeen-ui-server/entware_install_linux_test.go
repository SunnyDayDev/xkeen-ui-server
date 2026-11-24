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
	"sync/atomic"
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
	const nextTag = "v0.0.14"
	const invalidTag = "v0.0.15"
	const crashTag = "v0.0.16"
	const formatName = "agent-state-format.txt"
	mirrorFiles := t.TempDir()
	sums := map[string]string{}
	for _, version := range []string{tag, nextTag, invalidTag, crashTag} {
		dir := filepath.Join(mirrorFiles, version)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		var manifest strings.Builder
		for _, name := range []string{"xkeen-entware-installer-linux-arm64", "xkeen-agent-linux-arm64", formatName} {
			var raw []byte
			if name == formatName {
				if version == tag {
					continue
				}
				raw = []byte("agent-transport-state-v1\n")
			} else {
				source := name
				if version == invalidTag && name == "xkeen-agent-linux-arm64" {
					source = "xkeen-entware-installer-linux-arm64"
				}
				var err error
				raw, err = os.ReadFile(filepath.Join("/proof", source))
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&manifest, "%x  %s\n", sha256.Sum256(raw), name)
		}
		if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(manifest.String()), 0600); err != nil {
			t.Fatal(err)
		}
		sums[version] = fmt.Sprintf("%x", sha256.Sum256([]byte(manifest.String())))
	}
	var failTransfer atomic.Bool
	mirror := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, ok := strings.CutPrefix(r.URL.Path, "/releases/download/")
		version, name, found := strings.Cut(path, "/")
		if !ok || !found || sums[version] == "" {
			http.NotFound(w, r)
			return
		}
		switch name {
		case "SHA256SUMS", "xkeen-entware-installer-linux-arm64", "xkeen-agent-linux-arm64", formatName:
			if version == nextTag && name == "xkeen-agent-linux-arm64" && failTransfer.Load() {
				w.Header().Set("Content-Length", "131072")
				fmt.Fprint(w, "truncated file")
				return
			}
			http.ServeFile(w, r, filepath.Join(mirrorFiles, version, name))
		default:
			http.NotFound(w, r)
		}
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
			sums[tag], "--run-as", "65532:65532",
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
	// The authenticated process may already be durable when registration of
	// the init script was interrupted. Recover the exact attempt without restart.
	const processReceipt = "/opt/var/lib/xkeen-ui/install/agent-transport.process.json"
	beforeRetry, err := os.ReadFile(processReceipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(initPath); err != nil {
		t.Fatal(err)
	}
	if output, err := install("agent-transport", origin, "/nonexistent", cert); err != nil {
		t.Fatalf("confirmed process did not recover its init registration: %v, %s", err, output)
	}
	afterRetry, err := os.ReadFile(processReceipt)
	if err != nil || string(afterRetry) != string(beforeRetry) {
		t.Fatal("registration retry replaced the live agent")
	}

	invoke("status")
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
	const installedCA = "/opt/etc/xkeen-ui/agent-transport/ca.pem"
	ca, err := os.ReadFile(installedCA)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installedCA, append(append([]byte(nil), ca...), '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	changed := exec.CommandContext(t.Context(), initPath, "start")
	changedOutput, err := changed.CombinedOutput()
	if restoreErr := os.WriteFile(installedCA, ca, 0644); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if err == nil || !strings.Contains(string(changedOutput), "agent CA binding") {
		t.Fatalf("init accepted changed CA contents: %v, %s", err, changedOutput)
	}

	invoke("start")
	invoke("status")
	update := func(version string) ([]byte, error) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "/opt/bin/sh", script, "update", "--role", "agent-transport",
			"--version", version, "--manifest-sha256", sums[version], "--release-mirror", mirror.URL, "--mirror-ca-file", cert)
		return cmd.CombinedOutput()
	}
	receipt := func() string {
		t.Helper()
		bytes, err := os.ReadFile(processReceipt)
		if err != nil {
			t.Fatal(err)
		}
		return string(bytes)
	}
	generation := func() string {
		t.Helper()
		target, err := os.Readlink("/opt/libexec/xkeen-ui/agent-transport/current")
		if err != nil {
			t.Fatal(err)
		}
		return target
	}
	previous := receipt()
	failTransfer.Store(true)
	if out, err := update(nextTag); err == nil || !strings.Contains(string(out), "cannot download selected release asset") {
		t.Fatalf("truncated release did not fail before install: %v, %s", err, out)
	}
	failTransfer.Store(false)
	if receipt() != previous || generation() != "generations/"+tag {
		t.Fatal("failed transfer changed the running instance")
	}
	if out, err := update(tag); err != nil || receipt() != previous {
		t.Fatalf("exact rollout repeat restarted or failed: %v, %s", err, out)
	}
	if out, err := update(nextTag); err != nil {
		t.Fatalf("verified successor failed: %v, %s", err, out)
	}
	if receipt() == previous || generation() != "generations/"+nextTag {
		t.Fatal("new authenticated generation was not selected")
	}
	if out, err := update(invalidTag); err == nil || !strings.Contains(string(out), "previous generation restored") {
		t.Fatalf("unready successor did not restore previous: %v, %s", err, out)
	}
	if generation() != "generations/"+nextTag {
		t.Fatal("failed update left wrong active executable generation")
	}
	// The release is already verified. Interrupt the actual update controller
	// after it starts its new child while the central TLS endpoint is paused.
	stage, err := os.MkdirTemp("/opt/tmp", "xkeen-interrupted-update-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	for _, asset := range []string{"xkeen-entware-installer-linux-arm64", "xkeen-agent-linux-arm64", formatName} {
		raw, err := os.ReadFile(filepath.Join(mirrorFiles, crashTag, asset))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0600)
		if asset == "xkeen-entware-installer-linux-arm64" {
			mode = 0700
		}
		if err := os.WriteFile(filepath.Join(stage, asset), raw, mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := server.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	defer server.cmd.Process.Signal(syscall.SIGCONT)
	interrupted := exec.CommandContext(t.Context(), filepath.Join(stage, "xkeen-entware-installer-linux-arm64"),
		"update-stage", stage, crashTag, sums[crashTag])
	if err := interrupted.Start(); err != nil {
		t.Fatal(err)
	}
	defer interrupted.Process.Kill()
	deadline := time.Now().Add(4 * time.Second)
	seen := false
	for !time.Now().After(deadline) {
		raw, _ := os.ReadFile(processReceipt)
		var pending struct {
			PID     int
			Pending bool
		}
		if generation() == "generations/"+crashTag && json.Unmarshal(raw, &pending) == nil && pending.PID > 1 && pending.Pending {
			seen = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !seen {
		t.Fatal("candidate launch did not persist its process identity before readiness")
	}
	if err := interrupted.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := interrupted.Wait(); err == nil {
		t.Fatal("interrupted updater exited successfully")
	}
	if err := server.cmd.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	invoke("start")
	if generation() != "generations/"+nextTag {
		t.Fatal("startup did not restore prior generation")
	}
	invoke("status")
	invoke("stop")
	if got := readState(); got != first {
		t.Fatal("update and rollback changed saved identity")
	}

	invoke("start")
	remove := func() ([]byte, error) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "/opt/bin/sh", script, "uninstall", "--role", "agent-transport",
			"--version", nextTag, "--manifest-sha256", sums[nextTag], "--release-mirror", mirror.URL, "--mirror-ca-file", cert, "--confirm", "remove-service")
		return cmd.CombinedOutput()
	}
	goodReceipt := receipt()
	if err := os.WriteFile(processReceipt, wrongReceipt, 0600); err != nil {
		t.Fatal(err)
	}
	output, err = remove()
	if restoreErr := os.WriteFile(processReceipt, []byte(goodReceipt), 0600); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if err == nil || !strings.Contains(string(output), "instance ownership") || foreign.Process.Signal(syscall.Signal(0)) != nil {
		t.Fatalf("uninstall did not safely refuse foreign receipt: %v, %s", err, output)
	}
	invoke("status")
	const foreignFile = "/opt/libexec/xkeen-ui/agent-transport/foreign.bin"
	if err := os.WriteFile(foreignFile, []byte("foreign fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	if output, err := remove(); err == nil {
		t.Fatalf("uninstall accepted unmanaged content: %s", output)
	}
	invoke("status")
	if observed, err := os.ReadFile(foreignFile); err != nil || string(observed) != "foreign fixture" {
		t.Fatal("foreign content changed")
	}
	if err := os.Remove(foreignFile); err != nil {
		t.Fatal(err)
	}
	unknownGeneration := "/opt/libexec/xkeen-ui/agent-transport/generations/v91.1.1"
	if err := os.Mkdir(unknownGeneration, 0755); err != nil {
		t.Fatal(err)
	}
	unowned := filepath.Join(unknownGeneration, "xkeen-agent-linux-arm64")
	if err := os.WriteFile(unowned, []byte("unmanaged generation"), 0755); err != nil {
		t.Fatal(err)
	}
	if output, err := remove(); err == nil {
		t.Fatalf("uninstall accepted unregistered generation: %s", output)
	}
	invoke("status")
	if observed, err := os.ReadFile(unowned); err != nil || string(observed) != "unmanaged generation" {
		t.Fatal("unregistered generation changed")
	}
	if err := os.Remove(unowned); err != nil || os.Remove(unknownGeneration) != nil {
		t.Fatal("cannot remove fixture generation")
	}
	invoke("stop")
	stateLock, err := os.Open(filepath.Join(stateDir, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer stateLock.Close()
	if err := syscall.Flock(int(stateLock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if output, err := remove(); err == nil || !strings.Contains(string(output), "instance ownership") {
		t.Fatalf("uninstall ignored unidentified owner of state: %v, %s", err, output)
	}
	if err := exec.CommandContext(t.Context(), initPath, "start").Run(); err == nil {
		t.Fatal("interrupted uninstall allowed startup")
	}
	if err := stateLock.Close(); err != nil {
		t.Fatal(err)
	}

	const removalMarker = "/opt/var/lib/xkeen-ui/install/agent-transport.remove.json"
	marker, err := os.ReadFile(removalMarker)
	if err != nil || os.Remove(removalMarker) != nil {
		t.Fatal("cannot simulate unavailable durable marker")
	}
	legacyPath := filepath.Join("/opt/libexec/xkeen-ui/agent-transport/generations", nextTag, "xkeen-entware-installer-linux-arm64")
	if err := exec.CommandContext(t.Context(), legacyPath, "start").Run(); err == nil {
		t.Fatal("legacy direct launcher bypassed the held current path")
	}
	if err := os.WriteFile(removalMarker, marker, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := remove(); err != nil {
		t.Fatalf("transport uninstall failed: %v, %s", err, output)
	}
	for _, path := range []string{initPath, processReceipt, "/opt/libexec/xkeen-ui/agent-transport"} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("uninstall left managed file: %s", path)
		}
	}
	if readState() != first || foreign.Process.Signal(syscall.Signal(0)) != nil {
		t.Fatal("uninstall changed agent state or signalled a foreign process")
	}
	if output, err := remove(); err != nil {
		t.Fatalf("uninstall exact retry failed: %v, %s", err, output)
	}
	cmd = exec.CommandContext(t.Context(), "/opt/bin/sh", script, "install", "--role", "agent-transport",
		"--version", nextTag, "--manifest-sha256", sums[nextTag], "--release-mirror", mirror.URL, "--mirror-ca-file", cert,
		"--run-as", "65532:65532", "--server", origin, "--ca-file", cert)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reinstall with preserved identity failed: %v, %s", err, output)
	}
	invoke("status")
	invoke("stop")
	if readState() != first {
		t.Fatal("reinstall changed saved transport credentials")
	}

	identity, err := credentials.Identity(t.Context(), first.Key)
	if err != nil || identity.Generation != 1 || identity.RouterID != first.RouterID {
		t.Fatal("install or init.d restart created another registration")
	}
}
