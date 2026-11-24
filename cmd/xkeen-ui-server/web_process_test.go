package main

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func TestProcessWebRejectsOriginWithoutTLS(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.db")
	_, stderr, code := runBinaryInDir(t, dir, "--listen", "127.0.0.1:0", "--db-path", path, "--web-origin", "https://admin.example")
	if code != 2 || !strings.Contains(stderr, "TLS") {
		t.Fatalf("missing TLS reported status %d: %s", code, stderr)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid configuration reached storage: %v", err)
	}
}

func TestProcessWebAdminRejectsUnusableDatabasePaths(t *testing.T) {
	dir := t.TempDir()
	password := filepath.Join(dir, "password")
	if err := os.WriteFile(password, []byte("synthetic private password"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []struct{ action, path string }{
		{"reset", filepath.Join(dir, "missing.sqlite")},
		{"setup", filepath.Join(dir, "missing-directory", "server.sqlite")},
	} {
		_, _, status := runBinaryInDir(t, dir, "admin", command.action, "--db-path", command.path, "--password-file", password)
		if status != 1 {
			t.Fatalf("operation %s status = %d", command.action, status)
		}
		if _, err := os.Stat(command.path); !os.IsNotExist(err) {
			t.Fatalf("operation %s created a stray database: %v", command.action, err)
		}
	}
}

func TestProcessWebAdministratorLifecycle(t *testing.T) {
	const authority = "admin.example"
	const origin = "https://" + authority
	const pass = "synthetic first password"
	const replacement = "synthetic second password"
	dir := t.TempDir()
	path := filepath.Join(dir, "server.db")
	secret := filepath.Join(dir, "password")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(secret, []byte(s+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	change := func(action string, code int) {
		t.Helper()
		out, stderr, status := runBinaryInDir(t, dir, "admin", action, "--db-path", path, "--password-file", secret)
		if status != code || strings.Contains(out+stderr, pass) || strings.Contains(out+stderr, replacement) {
			t.Fatalf("unsafe administrator command result: status %d, want %d", status, code)
		}
	}
	cert, key, roots := fixtureServerCertificate(t)
	client := &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	defer client.CloseIdleConnections()
	launch := func() (*process, string) {
		p := startProcess(t, testBinary, "--listen", "127.0.0.1:0", "--db-path", path, "--tls-cert", cert, "--tls-key", key, "--web-origin", origin)
		return p, "https://" + p.readyWithClient(t, client, "https")
	}
	p, endpoint := launch()
	type templateSummary struct {
		ID      string
		Version string
	}
	type result struct {
		State     string
		CSRF      string
		Templates []templateSummary
	}
	request := func(method, path, requestOrigin, csrf, body string, cookie *http.Cookie, status int, state string) (result, *http.Cookie) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, endpoint+"/ui/v1"+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = authority
		if requestOrigin != "" {
			req.Header.Set("Origin", requestOrigin)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var res result
		if err := json.NewDecoder(response.Body).Decode(&res); err != nil {
			t.Fatalf("invalid session response (%d): %v", response.StatusCode, err)
		}
		if response.StatusCode != status || res.State != state || (state == "active") != (res.CSRF != "") || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("request %s %s => %d %q", method, path, response.StatusCode, res.State)
		}
		var issued *http.Cookie
		if cookies := response.Cookies(); len(cookies) == 1 {
			issued = cookies[0]
		} else if len(cookies) != 0 {
			t.Fatal("ambiguous response cookie")
		}
		return res, issued
	}
	login := func(password string, prior *http.Cookie) (result, *http.Cookie) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"password": password})
		res, issued := request("POST", "/login", origin, "", string(body), prior, 200, "active")
		if issued == nil || issued.Name != "__Host-xkeen_session" || !issued.Secure || !issued.HttpOnly || issued.Domain != "" || issued.Path != "/" || issued.SameSite != http.SameSiteLaxMode || issued.MaxAge != 43200 {
			t.Fatal("invalid issued session cookie")
		}
		return res, issued
	}
	request("GET", "/session", "", "", "", nil, 200, "setup_required")
	write(pass)
	change("reset", 1)
	change("setup", 0)
	change("setup", 1)
	request("GET", "/session", "", "", "", nil, 200, "signed_out")
	request("POST", "/login", "https://foreign.example", "", `{"password":"synthetic first password"}`, nil, 403, "invalid_origin")
	first, firstCookie := login(pass, nil)
	listing, _ := request("GET", "/templates", "", "", "", firstCookie, 200, "")
	if listing.Templates == nil || len(listing.Templates) != 0 {
		t.Fatalf("expected empty array, got %#v", listing.Templates)
	}
	db, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	const templateID = " branch / # "
	_, buildErr := templates.New(sqlite.NewTemplateRepository(db)).Create(t.Context(), configuration.TemplateDraft{ID: templateID})
	if err := db.Close(); buildErr != nil || err != nil {
		t.Fatalf("seeding C02 draft: %v, %v", buildErr, err)
	}
	listing, _ = request("GET", "/templates", "", "", "", firstCookie, 200, "")
	if len(listing.Templates) != 1 || listing.Templates[0] != (templateSummary{ID: templateID, Version: "1"}) {
		t.Fatalf("expected saved C02 metadata, got %#v", listing.Templates)
	}
	agentRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint+"/agent/v1/identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	agentRequest.AddCookie(firstCookie)
	agentRequest.Header.Set("X-CSRF-Token", first.CSRF)
	agentResponse, err := client.Do(agentRequest)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, agentResponse.Body)
	agentResponse.Body.Close()
	if agentResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("browser session reached agent authority: %d", agentResponse.StatusCode)
	}
	second, secondCookie := login(pass, nil)
	request("POST", "/logout", origin, second.CSRF, "", firstCookie, 403, "invalid_csrf")
	request("POST", "/session/activity", origin, first.CSRF, "", firstCookie, 200, "active")
	client.CloseIdleConnections()
	stopAgentProcess(t, p)
	p, endpoint = launch()
	reopened, _ := request("GET", "/session", "", "", "", firstCookie, 200, "active")
	if reopened.CSRF != first.CSRF {
		t.Fatal("session changed during restart")
	}
	rotated, rotatedCookie := login(pass, firstCookie)
	if rotatedCookie.Value == firstCookie.Value || rotated.CSRF == first.CSRF {
		t.Fatal("unchanged login credential")
	}
	request("GET", "/session", "", "", "", firstCookie, 200, "signed_out")
	request("POST", "/logout", origin, rotated.CSRF, "", rotatedCookie, 200, "signed_out")
	request("GET", "/session", "", "", "", secondCookie, 200, "active")
	write("too short")
	change("reset", 1)
	request("GET", "/session", "", "", "", secondCookie, 200, "active")
	write(replacement)
	change("reset", 0)
	request("GET", "/session", "", "", "", secondCookie, 200, "signed_out")
	request("GET", "/templates", "", "", "", secondCookie, 401, "signed_out")
	request("POST", "/login", origin, "", `{"password":"synthetic first password"}`, nil, 401, "invalid_credentials")
	login(replacement, nil)
	// Exercise expensive incorrect passwords while the non-browser handler remains available.
	const rejectedLogin = `{"password":"synthetic incorrect password"}`
	start := make(chan struct{})
	statuses := make(chan int, 8)
	for i := 0; i < cap(statuses); i++ {
		go func() {
			<-start
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint+"/ui/v1/login", strings.NewReader(rejectedLogin))
			if err != nil {
				statuses <- 0
				return
			}
			req.Host = authority
			req.Header.Set("Origin", origin)
			req.Header.Set("Content-Type", "application/json")
			response, err := client.Do(req)
			if err != nil {
				statuses <- 0
				return
			}
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			statuses <- response.StatusCode
		}()
	}
	close(start)
	health, err := client.Get(endpoint + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("health during verification: %d", health.StatusCode)
	}
	throttled := 0
	for i := 0; i < cap(statuses); i++ {
		switch status := <-statuses; status {
		case http.StatusUnauthorized:
		case http.StatusTooManyRequests:
			throttled++
		default:
			t.Fatalf("parallel login status = %d", status)
		}
	}
	if throttled == 0 {
		t.Fatal("parallel invalid login was never throttled")
	}
	client.CloseIdleConnections()
	stopAgentProcess(t, p)
	if strings.Contains(p.output(), firstCookie.Value) || strings.Contains(p.output(), pass) || strings.Contains(p.output(), replacement) {
		t.Fatal("secret printed")
	}
}
