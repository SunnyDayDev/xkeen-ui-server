package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func TestProcessWebRouterPreparation(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	database := filepath.Join(dir, "server.sqlite")
	passwordFile := filepath.Join(dir, "password")
	const password = "synthetic router setup password"
	if err := os.WriteFile(passwordFile, []byte(password+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, status := runBinaryInDir(t, dir, "admin", "setup", "--db-path", database, "--password-file", passwordFile)
	if status != 0 {
		t.Fatal("administrator setup failed")
	}
	databaseHandle, err := sqlite.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer databaseHandle.Close()
	const templateID = "edge / example"
	const element = "550e8400-e29b-41d4-a716-446655440000"
	_, err = templates.New(sqlite.NewTemplateRepository(databaseHandle)).Create(ctx, configuration.TemplateDraft{ID: templateID, Sections: []configuration.TemplateSection{{
		Name: "outbounds", Elements: []configuration.TemplateElement{{Element: configuration.Element{
			ID: element, Content: `{"protocol":"${protocol}","tag":"${label}"}`,
		}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	cert, key, roots := fixtureServerCertificate(t)
	const authority = "admin.example"
	const origin = "https://" + authority
	socket := os.Getenv("XKEEN_TEST_F09_VALIDATOR_SOCKET")
	remote := os.Getenv("XKEEN_TEST_F09_VALIDATOR_ORIGIN")
	args := []string{"--listen", "127.0.0.1:0", "--db-path", database, "--tls-cert", cert, "--tls-key", key, "--web-origin", origin}
	if socket != "" {
		args = append(args, "--validator-socket", socket)
	} else if remote != "" {
		trust := os.Getenv("XKEEN_TEST_F09_VALIDATOR_TRUST")
		args = append(args, "--validator-origin", remote, "--validator-ca", filepath.Join(trust, "worker-ca.pem"),
			"--validator-client-cert", filepath.Join(trust, "client.pem"), "--validator-client-key", filepath.Join(trust, "client.key"))
	}
	p := startProcess(t, testBinary, args...)
	client := &http.Client{Timeout: 35 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	defer client.CloseIdleConnections()
	addr := p.readyWithClient(t, client, "https")
	var session *http.Cookie
	var csrf string
	request := func(method, path string, payload []byte, status int) ([]byte, *http.Response) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, "https://"+addr+path, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = authority
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
		if err != nil || res.StatusCode != status || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s => %d, %q, %v", method, path, res.StatusCode, raw, err)
		}
		return raw, res
	}
	const unpublishedID = "unassigned / example"
	if _, err := templates.New(sqlite.NewTemplateRepository(databaseHandle)).Create(ctx, configuration.TemplateDraft{ID: unpublishedID}); err != nil {
		t.Fatal(err)
	}
	publicationPath := "/ui/v1/templates/publication?templateId=" + url.QueryEscape(unpublishedID)
	request(http.MethodGet, publicationPath, nil, 401)
	const route = "/ui/v1/routers/prepare"
	selectedRoute := route + "?templateId=" + url.QueryEscape(templateID)
	request(http.MethodGet, selectedRoute, nil, 401)
	login, _ := json.Marshal(map[string]string{"password": password})
	raw, res := request(http.MethodPost, "/ui/v1/login", login, 200)
	var loginResult struct{ CSRF string }
	if json.Unmarshal(raw, &loginResult) != nil || loginResult.CSRF == "" || len(res.Cookies()) != 1 {
		t.Fatal("missing verified session")
	}
	csrf, session = loginResult.CSRF, res.Cookies()[0]
	initial, _ := request(http.MethodGet, publicationPath, nil, 200)
	if !strings.Contains(string(initial), `"acceptedVersion":"0"`) {
		t.Fatalf("unexpected initial publication: %s", initial)
	}
	request(http.MethodPost, publicationPath, []byte(`{"action":"preview","version":"1","receipt":null}`), 400)
	checked, _ := request(http.MethodPost, publicationPath, []byte(`{"action":"preview","version":"1"}`), 200)
	var result struct {
		Receipt string
		Members []any
	}
	if json.Unmarshal(checked, &result) != nil || result.Receipt == "" || result.Members == nil || len(result.Members) != 0 {
		t.Fatalf("missing zero-member preview: %s", checked)
	}
	beforeConfirmation, _ := request(http.MethodGet, publicationPath, nil, 200)
	if !bytes.Equal(initial, beforeConfirmation) {
		t.Fatalf("preview changed state: %s", beforeConfirmation)
	}
	request(http.MethodPost, publicationPath, []byte(`{"action":"confirm","receipt":"invalid"}`), 409)
	confirmation, _ := json.Marshal(map[string]string{"action": "confirm", "receipt": result.Receipt})
	committed, _ := request(http.MethodPost, publicationPath, confirmation, 200)
	if !strings.Contains(string(committed), `"acceptedVersion":"1"`) || !strings.Contains(string(committed), `"members":[]`) {
		t.Fatalf("unexpected zero-member commit: %s", committed)
	}
	request(http.MethodPost, publicationPath, confirmation, 409)

	request(http.MethodGet, "/ui/routers/connect", nil, 200)
	raw, _ = request(http.MethodGet, selectedRoute, nil, 200)
	var form struct{ RouterID, TemplateID, Source, Version string }
	if json.Unmarshal(raw, &form) != nil || !configuration.ValidElementID(form.RouterID) || form.TemplateID != templateID || form.Source != "draft" || form.Version != "1" ||
		!strings.Contains(string(raw), `"required":true`) {
		t.Fatalf("unexpected preparation form: %q", raw)
	}
	submit := func(protocol string, status int, state string) {
		t.Helper()
		values := []map[string]string{{"elementId": element, "name": "label", "source": `"example-direct"`}}
		if protocol != "" {
			values = append(values, map[string]string{"elementId": element, "name": "protocol", "source": "\"" + protocol + "\""})
		}
		payload, _ := json.Marshal(map[string]any{"routerId": form.RouterID, "templateId": templateID, "name": "Example router",
			"source": form.Source, "version": form.Version, "values": values})
		out, _ := request(http.MethodPost, route, payload, status)
		if state != "" && !strings.Contains(string(out), `"state":"`+state+`"`) {
			t.Fatalf("expected %s: %s", state, out)
		}
	}
	preparedRoute := route + "?routerId=" + url.QueryEscape(form.RouterID)
	submit("", 422, "invalid_input")
	request(http.MethodGet, preparedRoute, nil, 404)
	if socket == "" && remote == "" {
		submit("freedom", 503, "validator_unavailable")
		request(http.MethodGet, preparedRoute, nil, 404)
		return
	}
	submit("not-a-supported-protocol", 422, "catalog_rejected")
	request(http.MethodGet, preparedRoute, nil, 404)
	submit("freedom", 201, "")
	raw, _ = request(http.MethodGet, preparedRoute, nil, 200)
	if !strings.Contains(string(raw), `"revision":"1"`) {
		t.Fatalf("missing first revision: %s", raw)
	}
	saved, err := sqlite.NewPublications(databaseHandle).ReadDesired(ctx, form.RouterID)
	if err != nil || saved == nil || saved.Number != 1 {
		t.Fatalf("missing persisted first revision: %#v %v", saved, err)
	}
	for _, file := range saved.Catalog.Files {
		if strings.Contains(string(file.Content), "example-direct") {
			return
		}
	}
	t.Fatal("personal value missing from desired catalog")
}
