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
	"sync/atomic"
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

	const tag, nextTag, faultTag, incompatibleTag = "v0.0.15", "v0.0.16", "v0.0.17", "v0.0.18"
	matrix := os.Getenv("XKEEN_TEST_SERVER_MAINTENANCE_MATRIX") == "1"
	selected := tag
	files := t.TempDir()
	var manifest strings.Builder
	for _, name := range []string{"xkeen-entware-installer-linux-arm64", "xkeen-ui-server-linux-arm64"} {
		source := filepath.Join("/proof", name)
		if matrix && name == "xkeen-entware-installer-linux-arm64" {
			source = "/proof/recovery.test"
		}
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
		if !ok {
			name, ok = strings.CutPrefix(r.URL.Path, "/releases/download/"+nextTag+"/")
			if !ok {
				name, ok = strings.CutPrefix(r.URL.Path, "/releases/download/"+faultTag+"/")
				if !ok {
					name, ok = strings.CutPrefix(r.URL.Path, "/releases/download/"+incompatibleTag+"/")
				}
			}
		}
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

	var validationOptions []string
	var calls *atomic.Int64
	if os.Getenv("XKEEN_TEST_INSTALLED_VALIDATOR") == "1" {
		validationOptions, calls = entwareValidatorFixture(t)
	}
	const initPath = "/opt/etc/init.d/S91xkeen-ui-server"
	t.Cleanup(func() { _ = exec.Command(initPath, "stop").Run() })
	runInstall := func(keyPath, principal string, extra ...string) ([]byte, error) {
		t.Helper()
		args := []string{script, "install",
			"--role", "server", "--version", selected, "--manifest-sha256",
			fmt.Sprintf("%x", sha256.Sum256([]byte(manifest.String()))), "--run-as", principal,
			"--listen", listen, "--web-origin", origin, "--tls-cert", cert, "--tls-key", keyPath,
			"--release-mirror", mirror.URL, "--mirror-ca-file", cert}
		args = append(args, extra...)
		cmd := exec.CommandContext(t.Context(), "/opt/bin/sh", args...)
		return cmd.CombinedOutput()
	}
	if output, err := runInstall(otherKey, "65531:65531", validationOptions...); err == nil || !strings.Contains(string(output), "invalid server TLS pair") {
		t.Fatalf("mismatched TLS preflight: %v, %s", err, output)
	}
	if _, err := os.Stat(initPath); !os.IsNotExist(err) {
		t.Fatal("invalid TLS pair left server init registration")
	}

	if _, err := os.Stat("/opt/etc/xkeen-ui/agent-transport/profile.json"); err == nil {
		if output, err := runInstall(key, "65531:65532", validationOptions...); err == nil || !strings.Contains(string(output), "separate service identities") {
			t.Fatalf("shared role group was accepted: %v, %s", err, output)
		}
	}

	if output, err := runInstall(key, "65531:65531", "--validator-origin", "https://validator.example"); err == nil ||
		!strings.Contains(string(output), "complete external validator configuration") {
		t.Fatalf("incomplete external configuration: %v, %s", err, output)
	}

	web := &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{
		DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}}
	defer web.CloseIdleConnections()
	foreign := startProcess(t, testBinary, "--db-path", filepath.Join(t.TempDir(), "foreign.sqlite"), "--listen", endpoint,
		"--tls-cert", cert, "--tls-key", key, "--web-origin", origin)
	foreign.readyWithClient(t, web, "https")
	if output, err := runInstall(key, "65531:65531", validationOptions...); err == nil || !strings.Contains(string(output), "server did not confirm HTTPS readiness") {
		t.Fatalf("occupied TLS endpoint accepted: %v, %s", err, output)
	}
	if _, err := os.Stat(initPath); !os.IsNotExist(err) || foreign.cmd.Process.Signal(syscall.Signal(0)) != nil {
		t.Fatal("occupied port changed foreign listener or registered a service")
	}
	stopRouterAgent(t, foreign)

	if output, err := runInstall(key, "65531:65531", validationOptions...); err != nil {
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
	login := func() (*http.Cookie, string) {
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
		var result struct{ CSRF string }
		if err := json.NewDecoder(res.Body).Decode(&result); err != nil || result.CSRF == "" {
			t.Fatal("missing CSRF proof")
		}
		return res.Cookies()[0], result.CSRF
	}
	cookie, csrf := login()
	want := http.StatusServiceUnavailable
	if calls != nil {
		want = http.StatusCreated
	}
	exerciseInstalledPreparation(t, web, origin, cookie, csrf, want)
	if calls != nil && calls.Load() != 1 {
		t.Fatal("configured mTLS worker did not handle exactly one publication")
	}

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
	if calls != nil {
		const installedKey = "/opt/etc/xkeen-ui/server/validator-client-key.pem"
		info, err := os.Stat(installedKey)
		if err != nil || info.Mode().Perm() != 0640 || info.Sys().(*syscall.Stat_t).Uid != 0 || info.Sys().(*syscall.Stat_t).Gid != 65531 {
			t.Fatal("installed worker key has unexpected access")
		}
		original, err := os.ReadFile(installedKey)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(installedKey, append(append([]byte(nil), original...), '\n'), 0640); err != nil {
			t.Fatal(err)
		}
		out, startErr := exec.CommandContext(t.Context(), initPath, "start").CombinedOutput()
		if err := os.WriteFile(installedKey, original, 0640); err != nil {
			t.Fatal(err)
		}
		if startErr == nil || !strings.Contains(string(out), "invalid external validator trust") {
			t.Fatal("modified worker key was accepted")
		}
	}

	invoke("start")
	invoke("status")
	login()
	after, err := os.Stat(dbPath)
	if err != nil || !os.SameFile(db, after) {
		t.Fatal("server restart replaced SQLite")
	}
	activeCookie, activeCSRF := login()
	serverCommand := func(action string, extra ...string) *exec.Cmd {
		t.Helper()
		args := []string{script, action, "--role", "server", "--version", selected, "--manifest-sha256",
			fmt.Sprintf("%x", sha256.Sum256([]byte(manifest.String()))), "--release-mirror", mirror.URL, "--mirror-ca-file", cert}
		args = append(args, extra...)
		return exec.CommandContext(t.Context(), "/opt/bin/sh", args...)
	}
	serverOperation := func(action string, extra ...string) ([]byte, error) {
		return serverCommand(action, extra...).CombinedOutput()
	}
	const item = "/ui/v1/templates/item?templateId=late-maintenance"
	webOperation := func(method, url, host, body, proof string, token *http.Cookie, want int) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		if method == http.MethodPost {
			req.Header.Set("Origin", origin)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", proof)
		}
		req.AddCookie(token)
		res, err := web.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if err != nil || res.StatusCode != want {
			t.Fatalf("server maintenance request got %d, want %d", res.StatusCode, want)
		}
		return string(raw)
	}
	verifyLateState := func(base string, revoked *http.Cookie) {
		t.Helper()
		raw := webOperation(http.MethodGet, base+item, endpoint, "", "", activeCookie, 200)
		if !strings.Contains(raw, `"version":"2"`) || !strings.Contains(raw, "late-maintenance-marker") {
			t.Fatal("late draft edit was lost")
		}
		webOperation(http.MethodGet, base+item, endpoint, "", "", revoked, 401)
	}
	selected = nextTag
	if matrix {
		revoked, revokedCSRF := login()
		webOperation(http.MethodPost, origin+"/ui/v1/templates", endpoint, `{"id":"late-maintenance"}`, activeCSRF, activeCookie, 201)
		checkpoint := t.TempDir()
		update := serverCommand("update")
		update.Env = append(os.Environ(), "XKEEN_TEST_SERVER_MAINTENANCE_CHECKPOINT="+checkpoint)
		var output strings.Builder
		update.Stdout, update.Stderr = &output, &output
		if err := update.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = update.Process.Kill(); _ = update.Wait() }()
		waitRecovery(t, 10*time.Second, "first snapshot was not validated", func() bool {
			_, err := os.Stat(filepath.Join(checkpoint, "preflight-completed"))
			return err == nil
		})
		webOperation(http.MethodPost, origin+item, endpoint,
			`{"version":"1","section":"outbounds","source":"{\"tag\":\"late-maintenance-marker\"}"}`,
			activeCSRF, activeCookie, 200)
		webOperation(http.MethodPost, origin+"/ui/v1/logout", endpoint, "", revokedCSRF, revoked, 200)
		if err := os.WriteFile(filepath.Join(checkpoint, "client-completed"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		if err := update.Wait(); err != nil {
			t.Fatalf("installed server update: %v, %s", err, output.String())
		}
		verifyLateState(origin, revoked)
		backups, err := filepath.Glob(filepath.Join(state, "backup-*", "server.sqlite"))
		if err != nil || len(backups) != 1 {
			t.Fatal("cannot find the final backup")
		}
		p := startProcess(t, testBinary, "--db-path", backups[0], "--listen", "127.0.0.1:0",
			"--tls-cert", cert, "--tls-key", key, "--web-origin", origin)
		verifyLateState("https://"+p.readyWithClient(t, web, "https"), revoked)
		stopAgentProcess(t, p)

		selected = faultTag
		fault := serverCommand("update")
		fault.Env = append(os.Environ(), "XKEEN_TEST_INTERRUPT_SERVER_ADMISSION=1")
		if out, err := fault.CombinedOutput(); err == nil || !strings.Contains(string(out), "previous server generation restored") {
			t.Fatalf("failed same-format candidate did not recover: %v, %s", err, out)
		}
		verifyLateState(origin, revoked)

		// Hold a different TLS listener through the genuine candidate start.
		// Recovery must retain the previous scope until ownership can be proven.
		admission := t.TempDir()
		fault = serverCommand("update")
		fault.Env = append(os.Environ(), "XKEEN_TEST_SERVER_ADMISSION_CHECKPOINT="+admission)
		var faultOutput strings.Builder
		fault.Stdout, fault.Stderr = &faultOutput, &faultOutput
		if err := fault.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = fault.Process.Kill(); _ = fault.Wait() }()
		waitRecovery(t, 10*time.Second, "candidate did not reach admission", func() bool {
			_, err := os.Stat(filepath.Join(admission, "candidate-admitted"))
			return err == nil
		})
		occupied, err := net.Listen("tcp", endpoint)
		if err != nil {
			t.Fatal(err)
		}
		foreign := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		foreign.Listener.Close()
		foreign.Listener = occupied
		if err := startEntwareFixtureTLS(foreign, cert, key); err != nil {
			t.Fatal(err)
		}
		defer foreign.Close()
		if err := os.WriteFile(filepath.Join(admission, "client-completed"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		if err := fault.Wait(); err == nil || !strings.Contains(faultOutput.String(), "did not confirm HTTPS readiness") {
			t.Fatalf("genuine candidate start did not reject unrelated TLS: %v, %s", err, faultOutput.String())
		}
		query("/healthz")
		foreign.Close()
		invoke("start")
		verifyLateState(origin, revoked)
		selected = nextTag
	} else if out, err := serverOperation("update"); err != nil {
		t.Fatalf("installed server update: %v, %s", err, out)
	}
	if target, err := os.Readlink("/opt/libexec/xkeen-ui/server/current"); err != nil || target != "generations/"+nextTag {
		t.Fatalf("wrong active server release: %q, %v", target, err)
	}
	invoke("status")
	remove := func() ([]byte, error) { return serverOperation("uninstall", "--confirm", "remove-service") }
	const existingKey = "/opt/etc/xkeen-ui/server/tls-key.pem"
	originalKey, err := os.ReadFile(existingKey)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := remove(); err != nil {
		t.Fatalf("installed server uninstall: %v, %s", err, out)
	}
	for _, path := range []string{initPath, "/opt/libexec/xkeen-ui/server", "/opt/var/lib/xkeen-ui/install/server.process.json"} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("server uninstall left owned file: %s", path)
		}
	}
	after, err = os.Stat(dbPath)
	remainingKey, keyErr := os.ReadFile(existingKey)
	if err != nil || !os.SameFile(db, after) || keyErr != nil || string(remainingKey) != string(originalKey) {
		t.Fatal("server uninstall replaced SQLite or TLS key")
	}
	if out, err := remove(); err != nil {
		t.Fatalf("server uninstall repeat: %v, %s", err, out)
	}
	if output, err := runInstall(key, "65531:65531", validationOptions...); err != nil {
		t.Fatalf("server reinstall: %v, %s", err, output)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, origin+"/ui/v1/templates", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(activeCookie)
	response, err := web.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("retained administrator session returned %d after reinstall", response.StatusCode)
	}

	if matrix {
		selected = incompatibleTag
		fault := serverCommand("update")
		fault.Env = append(os.Environ(), "XKEEN_TEST_INCOMPATIBLE_SERVER_ADMISSION=1")
		if out, err := fault.CombinedOutput(); err == nil || !strings.Contains(string(out), "maintenance requires local recovery") {
			t.Fatalf("incompatible candidate was not held: %v, %s", err, out)
		}
		if out, err := exec.CommandContext(t.Context(), initPath, "start").CombinedOutput(); err == nil ||
			!strings.Contains(string(out), "maintenance requires local recovery") {
			t.Fatalf("normal startup bypassed incompatible update guard: %v, %s", err, out)
		}
		if conn, err := net.DialTimeout("tcp", endpoint, time.Second); err == nil {
			conn.Close()
			t.Fatal("automatic restart opened an unverified server")
		}
		actual, err := os.Stat(dbPath)
		if err != nil || !os.SameFile(db, actual) {
			t.Fatal("incompatible iteration replaced the live database with an old backup")
		}
		if actual, err := os.ReadFile(existingKey); err != nil || string(actual) != string(originalKey) {
			t.Fatal("incompatible iteration modified the TLS key")
		}
	}

}
