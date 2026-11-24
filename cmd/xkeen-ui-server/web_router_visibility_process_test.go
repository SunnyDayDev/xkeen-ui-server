package main

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
)

func TestProcessWebRouterVisibility(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "server.sqlite")
	invite, credentials := fixtureEnrollment(t, database)
	working := agentcredential.Generate(agentcredential.Working).Encode()
	id, err := credentials.Enroll(t.Context(), invite, working)
	if err != nil {
		t.Fatal(err)
	}

	const password = "synthetic visibility password"
	passfile := filepath.Join(dir, "password")
	if err := os.WriteFile(passfile, []byte(password+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	admin := func(command string) {
		t.Helper()
		if _, _, code := runBinaryInDir(t, dir, "admin", command, "--db-path", database, "--password-file", passfile); code != 0 {
			t.Fatal("administrator command failed")
		}
	}
	admin("setup")
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := reservation.Addr().String()
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	origin := "https://" + listen
	cert, key, roots := fixtureServerCertificate(t)
	process := startProcess(t, testBinary, "--listen", listen, "--db-path", database, "--tls-cert", cert, "--tls-key", key, "--web-origin", origin, "--agent-origin", origin)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	defer client.CloseIdleConnections()
	process.readyWithClient(t, client, "https")
	path := "/ui/v1/routers/visibility?" + url.Values{"routerId": {id.RouterID}}.Encode()
	var session *http.Cookie
	var csrf string
	request := func(method, path, body string, agent bool, want int) ([]byte, *http.Response) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, origin+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if agent {
			req.Header.Set("Authorization", "Bearer "+working)
		} else if session != nil {
			req.AddCookie(session)
		}
		if method == http.MethodPost && !agent {
			req.Header.Set("Origin", origin)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", csrf)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != want {
			t.Fatalf("%s %s got %d want %d", method, path, response.StatusCode, want)
		}
		return raw, response
	}
	login := func() {
		raw, response := request(http.MethodPost, "/ui/v1/login", `{"password":"`+password+`"}`, false, 200)
		var signedIn struct{ CSRF string }
		if json.Unmarshal(raw, &signedIn) != nil || signedIn.CSRF == "" || len(response.Cookies()) != 1 {
			t.Fatal("missing session")
		}
		session, csrf = response.Cookies()[0], signedIn.CSRF
	}
	snapshot := func(visible bool) string {
		t.Helper()
		raw, _ := request(http.MethodGet, path, "", false, 200)
		var value struct {
			RouterID, ActiveGeneration, Epoch string
			Visible                           bool
		}
		if json.Unmarshal(raw, &value) != nil || value.RouterID != id.RouterID || value.ActiveGeneration == "" || value.Epoch == "" || value.Visible != visible {
			t.Fatal("unexpected visibility snapshot")
		}
		body, _ := json.Marshal(map[string]any{"activeGeneration": value.ActiveGeneration, "epoch": value.Epoch, "confirm": true})
		return string(body)
	}
	overviewContains := func(want bool) {
		t.Helper()
		raw, _ := request(http.MethodGet, "/ui/v1/routers", "", false, 200)
		if strings.Contains(string(raw), id.RouterID) != want {
			t.Fatal("unexpected overview visibility")
		}
	}
	heartbeat := func() { request(http.MethodPost, "/agent/v1/contact", "", true, 204) }
	request(http.MethodGet, path, "", false, 401)
	login()
	request(http.MethodGet, path, "", false, 404)
	heartbeat()
	first := snapshot(true)
	firstSession, firstProof := session, csrf
	session, csrf = nil, ""
	login()
	secondSession, secondProof := session, csrf
	if secondSession.Value == firstSession.Value {
		t.Fatal("two windows received the same session")
	}
	secondBasis := snapshot(true)
	session, csrf = firstSession, firstProof

	request(http.MethodPost, path, `{"activeGeneration":"1","epoch":"0"}`, false, 400)
	heartbeat()
	request(http.MethodPost, path, first, false, 200)
	overviewContains(false)
	snapshot(false)
	session, csrf = secondSession, secondProof
	request(http.MethodPost, path, secondBasis, false, 409)
	overviewContains(false)
	session, csrf = firstSession, firstProof

	heartbeat()
	overviewContains(true)
	second := snapshot(true)
	request(http.MethodPost, path, first, false, 409)
	overviewContains(true)
	admin("reset")
	request(http.MethodPost, path, second, false, 401)
	login()
	overviewContains(true)
	if got := snapshot(true); got != second {
		t.Fatal("revocation changed visibility")
	}
	if got, err := credentials.Identity(t.Context(), working); err != nil || got != id {
		t.Fatal("working identity changed")
	}
	stopAgentProcess(t, process)
}
