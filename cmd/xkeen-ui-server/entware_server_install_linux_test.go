package main

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestEntwareInstallServerKeepsTLSAndAdministrator(t *testing.T) {
	script := os.Getenv("XKEEN_TEST_ENTWARE_INSTALLER")
	if script == "" {
		t.Skip("run lab/check-entware-install.sh")
	}
	cert, key, roots := fixtureServerCertificate(t)
	_, otherKey, _ := fixtureServerCertificate(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	listen := fmt.Sprintf("0.0.0.0:%d", port)
	origin := "https://" + endpoint

	const tag = "v0.0.15"
	files := t.TempDir()
	var manifest strings.Builder
	for _, name := range []string{"xkeen-entware-installer-linux-arm64", "xkeen-ui-server-linux-arm64"} {
		source := filepath.Join("/proof", name)
		if name == "xkeen-ui-server-linux-arm64" {
			source = testBinary
		}
		raw, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(files, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&manifest, "%x  %s\n", sha256.Sum256(raw), name)
	}
	if err := os.WriteFile(filepath.Join(files, "SHA256SUMS"), []byte(manifest.String()), 0600); err != nil {
		t.Fatal(err)
	}
	mirror := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutPrefix(r.URL.Path, "/releases/download/"+tag+"/")
		if !ok || (name != "SHA256SUMS" && name != "xkeen-entware-installer-linux-arm64" && name != "xkeen-ui-server-linux-arm64") {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(files, name))
	}))
	if err := startEntwareFixtureTLS(mirror, cert, key); err != nil {
		t.Fatal(err)
	}
	defer mirror.Close()

	const initPath = "/opt/etc/init.d/S91xkeen-ui-server"
	t.Cleanup(func() { _ = exec.Command(initPath, "stop").Run() })
	runInstall := func(keyPath, principal string) ([]byte, error) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "/opt/bin/sh", script, "install",
			"--role", "server", "--version", tag, "--manifest-sha256",
			fmt.Sprintf("%x", sha256.Sum256([]byte(manifest.String()))), "--run-as", principal,
			"--listen", listen, "--web-origin", origin, "--tls-cert", cert, "--tls-key", keyPath,
			"--release-mirror", mirror.URL, "--mirror-ca-file", cert)
		return cmd.CombinedOutput()
	}
	if output, err := runInstall(otherKey, "65531:65531"); err == nil || !strings.Contains(string(output), "invalid server TLS pair") {
		t.Fatalf("mismatched TLS preflight: %v, %s", err, output)
	}
	if _, err := os.Stat(initPath); !os.IsNotExist(err) {
		t.Fatal("invalid TLS pair left server init registration")
	}

	if _, err := os.Stat("/opt/etc/xkeen-ui/agent-transport/profile.json"); err == nil {
		if output, err := runInstall(key, "65531:65532"); err == nil || !strings.Contains(string(output), "separate service identities") {
			t.Fatalf("shared role group was accepted: %v, %s", err, output)
		}
	}

	web := &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}}
	defer web.CloseIdleConnections()
	foreign := startProcess(t, testBinary, "--db-path", filepath.Join(t.TempDir(), "foreign.sqlite"), "--listen", endpoint,
		"--tls-cert", cert, "--tls-key", key, "--web-origin", origin)
	foreign.readyWithClient(t, web, "https")
	if output, err := runInstall(key, "65531:65531"); err == nil || !strings.Contains(string(output), "server did not confirm HTTPS readiness") {
		t.Fatalf("occupied TLS endpoint accepted: %v, %s", err, output)
	}
	if _, err := os.Stat(initPath); !os.IsNotExist(err) || foreign.cmd.Process.Signal(syscall.Signal(0)) != nil {
		t.Fatal("occupied port changed foreign listener or registered a service")
	}
	stopRouterAgent(t, foreign)

	if output, err := runInstall(key, "65531:65531"); err != nil {
		t.Fatalf("valid server installation: %v, %s", err, output)
	}
	invoke := func(args ...string) {
		t.Helper()
		if out, err := exec.CommandContext(t.Context(), initPath, args...).CombinedOutput(); err != nil {
			t.Fatalf("server service command: %v, %s", err, out)
		}
	}
	query := func(path string) []byte {
		t.Helper()
		res, err := web.Get(origin + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("server response for %s: %d %v", path, res.StatusCode, err)
		}
		return raw
	}
	invoke("status")
	query("/healthz")
	body := query("/ui/")
	asset := regexp.MustCompile(`/ui/assets/[a-zA-Z0-9._-]+\.js`).Find(body)
	if len(asset) == 0 || len(query(string(asset))) < 1000 {
		t.Fatal("installed server did not serve its embedded JavaScript")
	}

	const state = "/opt/var/lib/xkeen-ui/server"
	const syntheticPassword = "synthetic installed server administrator password"
	password := filepath.Join(state, "fixture-password")
	if err := os.WriteFile(password, []byte(syntheticPassword+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(password, 65531, 65531); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(password)
	invoke("admin", "setup", "--password-file", password)
	login := func() {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"password": syntheticPassword})
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, origin+"/ui/v1/login", strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		res, err := web.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 || len(res.Cookies()) != 1 || !res.Cookies()[0].Secure {
			t.Fatalf("installed administrator login: %d", res.StatusCode)
		}
	}
	login()
	dbPath := filepath.Join(state, "server.sqlite")
	db, err := os.Stat(dbPath)
	if err != nil || db.Sys().(*syscall.Stat_t).Uid != 65531 {
		t.Fatal("SQLite does not belong to the server user")
	}
	keyInfo, err := os.Stat("/opt/etc/xkeen-ui/server/tls-key.pem")
	if err != nil || keyInfo.Mode().Perm() != 0640 || keyInfo.Sys().(*syscall.Stat_t).Uid != 0 ||
		keyInfo.Sys().(*syscall.Stat_t).Gid != 65531 {
		t.Fatal("installed TLS key is not private to root and the server group")
	}
	invoke("stop")
	invoke("start")
	invoke("status")
	login()
	after, err := os.Stat(dbPath)
	if err != nil || !os.SameFile(db, after) {
		t.Fatal("server restart replaced SQLite")
	}
}
