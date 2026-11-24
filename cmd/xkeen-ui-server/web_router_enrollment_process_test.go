package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func TestProcessWebRouterEnrollment(t *testing.T) {
	agent := buildAgentForProcessTest(t)
	dir := t.TempDir()
	database := filepath.Join(dir, "server.sqlite")
	_, credentials := fixtureEnrollment(t, database)
	const elementID = "550e8400-e29b-41d4-a716-446655440000"
	db, err := sqlite.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	draft := []byte(`[{"name":"outbounds","elements":[{"disabled":false,"record":{"id":"` + elementID + `","content":"{\"tag\":\"example\",\"test\":\"$` + "{port}" + `\"}","variables":[{"name":"port","type":"number","default":443}]}}]}]`)
	if _, err := db.ExecContext(t.Context(), "UPDATE accepted_templates SET draft=?", draft); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := credentials.Revoke(t.Context(), "router-example"); err != nil {
		t.Fatal(err)
	}
	const password = "synthetic enrollment password"
	passwordPath := filepath.Join(dir, "password")
	if err := os.WriteFile(passwordPath, []byte(password+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, code := runBinaryInDir(t, dir, "admin", "setup", "--db-path", database, "--password-file", passwordPath); code != 0 {
		t.Fatal("administrator setup failed")
	}
	cert, key, roots := fixtureServerCertificate(t)
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := reservation.Addr().String()
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	origin := "https://" + listen
	server := startProcess(t, testBinary, "--listen", listen, "--db-path", database,
		"--tls-cert", cert, "--tls-key", key, "--web-origin", origin, "--agent-origin", origin)
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	defer client.CloseIdleConnections()
	server.readyWithClient(t, client, "https")
	var session *http.Cookie
	var csrf string
	request := func(method, path string, payload []byte, want int) ([]byte, *http.Response) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, origin+path, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		if session != nil {
			req.AddCookie(session)
		}
		if method == http.MethodPost {
			req.Header.Set("Origin", origin)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", csrf)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != want || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: status=%d error=%v", method, path, res.StatusCode, err)
		}
		return raw, res
	}
	const enrollment = "/ui/v1/routers/enroll?routerId=router-example"
	const overview = "/ui/v1/routers"
	request(http.MethodGet, overview, nil, http.StatusUnauthorized)
	request(http.MethodGet, enrollment, nil, http.StatusUnauthorized)
	const variablePath = "/ui/v1/routers/variables?routerId=router-example&elementId=" + elementID
	request(http.MethodGet, variablePath, nil, http.StatusUnauthorized)
	login, _ := json.Marshal(map[string]string{"password": password})
	body, res := request(http.MethodPost, "/ui/v1/login", login, http.StatusOK)
	var signedIn struct{ CSRF string }
	if json.Unmarshal(body, &signedIn) != nil || signedIn.CSRF == "" || len(res.Cookies()) != 1 {
		t.Fatal("missing administrator session")
	}
	session, csrf = res.Cookies()[0], signedIn.CSRF
	if raw, _ := request(http.MethodGet, overview, nil, http.StatusOK); !bytes.Equal(bytes.TrimSpace(raw), []byte(`{"routers":[]}`)) {
		t.Fatal("unobserved router entered the overview")
	}
	body, _ = request(http.MethodGet, enrollment, nil, http.StatusOK)
	var before struct{ Origin, Epoch, Phase, DesiredRevision, Contact string }
	if json.Unmarshal(body, &before) != nil || before.Origin != origin || before.Phase != "missing" ||
		before.Contact != "unobserved" || before.DesiredRevision != "1" {
		t.Fatal("unexpected initial enrollment state")
	}
	intent, _ := json.Marshal(map[string]string{"action": "issue", "epoch": before.Epoch, "desiredRevision": before.DesiredRevision})
	body, _ = request(http.MethodPost, enrollment, intent, http.StatusCreated)
	var receipt struct {
		Invitation string
		Expires    time.Time
	}
	if json.Unmarshal(body, &receipt) != nil || receipt.Invitation == "" || time.Until(receipt.Expires) < 14*time.Minute {
		t.Fatal("incomplete invitation receipt")
	}
	request(http.MethodPost, enrollment, intent, http.StatusConflict)
	body, _ = request(http.MethodGet, enrollment, nil, http.StatusOK)
	if bytes.Contains(body, []byte(receipt.Invitation)) || !bytes.Contains(body, []byte(`"phase":"pending"`)) {
		t.Fatal("unsafe pending metadata")
	}
	agentState := filepath.Join(dir, "agent")
	if err := os.Mkdir(agentState, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentState, "invitation"), []byte(receipt.Invitation+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	worker := startProcess(t, agent, "--server", origin, "--state-dir", agentState, "--ca-file", cert)
	waitForAgentAuthentication(t, worker)
	deadline := time.Now().Add(5 * time.Second)
	for {
		body, _ = request(http.MethodGet, overview, nil, http.StatusOK)
		if bytes.Contains(body, []byte(`"routerId":"router-example"`)) && bytes.Contains(body, []byte(`"contact":"online"`)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("accepted contact did not reach the protected overview")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if bytes.Contains(body, []byte(receipt.Invitation)) || bytes.Contains(body, []byte(`"appliedRevision"`)) {
		t.Fatal("overview exposed an invitation or fabricated applied evidence")
	}
	body, _ = request(http.MethodGet, variablePath, nil, http.StatusOK)
	if !bytes.Contains(body, []byte(`"defaultSource":"443"`)) || !bytes.Contains(body, []byte(`"draftGeneration":"0"`)) {
		t.Fatal("snapshot did not show the chosen variables and original generation")
	}
	source := " 9007199254740993\r\n"
	values := map[string]any{"templateId": "template-example", "activeGeneration": "1", "acceptedVersion": "1", "draftGeneration": "0",
		"values": []map[string]string{{"name": "port", "source": source}}}
	literal, _ := json.Marshal(values)
	body, _ = request(http.MethodPost, variablePath, literal, http.StatusOK)
	if !bytes.Contains(body, []byte(`"draftGeneration":"1"`)) || !bytes.Contains(body, []byte(`"personalSource":" 9007199254740993\r\n"`)) {
		t.Fatal("exact accepted personal source missing")
	}
	request(http.MethodPost, variablePath, literal, http.StatusConflict)
	values["draftGeneration"] = "1"
	values["values"] = []map[string]string{{"name": "port"}}
	inherited, _ := json.Marshal(values)
	body, _ = request(http.MethodPost, variablePath, inherited, http.StatusOK)
	if !bytes.Contains(body, []byte(`"draftGeneration":"2"`)) || bytes.Contains(body, []byte(`"personalSource"`)) {
		t.Fatal("explicit inheritance did not remove the stored personal source")
	}
	var desired int
	if err := db.QueryRowContext(t.Context(), "SELECT max(revision) FROM desired_router_revisions").Scan(&desired); err != nil || desired != 1 {
		t.Fatal("editing changed the desired revision")
	}
	stopRouterAgent(t, worker)
	body, _ = request(http.MethodGet, enrollment, nil, http.StatusOK)
	if bytes.Contains(body, []byte(receipt.Invitation)) || !bytes.Contains(body, []byte(`"phase":"registered"`)) ||
		bytes.Contains(body, []byte(`"appliedRevision"`)) {
		t.Fatal("registration unexpectedly exposed invitation or application")
	}
	if strings.Contains(server.output(), receipt.Invitation) || strings.Contains(worker.output(), receipt.Invitation) {
		t.Fatal("invitation exposed in process output")
	}
}
