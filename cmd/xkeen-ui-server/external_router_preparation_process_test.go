package main

import (
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

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func TestProcessExternalPreparationKeepsAgentAuthoritySeparate(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "server.sqlite")
	invitation, credentials := fixtureEnrollment(t, path)
	working := agentcredential.Generate(agentcredential.Working).Encode()
	enrolled, err := credentials.Enroll(ctx, invitation, working)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const (
		templateID    = "synthetic / external"
		elementID     = "550e8400-e29b-41d4-a716-446655440000"
		routerID      = "550e8400-e29b-41d4-a716-446655440001"
		rawCanary     = "synthetic-raw-canary"
		defaultCanary = "synthetic-default-canary"
	)
	_, err = templates.New(sqlite.NewTemplateRepository(db)).Create(ctx, configuration.TemplateDraft{
		ID: templateID, Sections: []configuration.TemplateSection{{Name: "outbounds", Elements: []configuration.TemplateElement{{Element: configuration.Element{
			ID: elementID, Content: `{"tag":"${label}","protocol":"${protocol}","settings":{"g189Canary":"` + rawCanary + `"}}`,
			Variables: []configuration.VariableDefinition{{Name: "label", Type: "string", Default: json.RawMessage(`"` + defaultCanary + `"`)},
				{Name: "protocol", Type: "string"}},
		}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE external_access SET enabled=1, protected=0, version=2"); err != nil {
		t.Fatal(err)
	}

	cert, key, roots := fixtureServerCertificate(t)
	args := []string{"--listen", "127.0.0.1:0", "--db-path", path, "--tls-cert", cert, "--tls-key", key}
	worker := os.Getenv("XKEEN_TEST_F09_VALIDATOR_ORIGIN")
	if worker != "" {
		trust := os.Getenv("XKEEN_TEST_F09_VALIDATOR_TRUST")
		args = append(args, "--validator-origin", worker, "--validator-ca", filepath.Join(trust, "worker-ca.pem"),
			"--validator-client-cert", filepath.Join(trust, "client.pem"), "--validator-client-key", filepath.Join(trust, "client.key"))
	}
	p := startProcess(t, testBinary, args...)
	client := &http.Client{Timeout: 35 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	defer client.CloseIdleConnections()
	query := url.Values{"routerId": {routerID}, "templateId": {templateID}}
	route := "https://" + p.readyWithClient(t, client, "https") + "/api/v1/routers/preparation?" + query.Encode()
	var bearer string
	call := func(method, tag, body string, status int) ([]byte, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, route, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("If-Match", tag)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != status || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s preparation got %d want %d", method, res.StatusCode, status)
		}
		if strings.Contains(string(raw), rawCanary) || strings.Contains(string(raw), defaultCanary) ||
			strings.Contains(string(raw), `"workingKey"`) || strings.Contains(string(raw), `"invitation"`) {
			t.Fatal("external preparation disclosed unauthorized values")
		}
		return raw, res.Header.Get("ETag")
	}
	assertAuthority := func() {
		t.Helper()
		var accessVersion, invitations, registrations int64
		err := db.QueryRowContext(ctx, `SELECT (SELECT version FROM external_access),
			(SELECT count(*) FROM agent_invitations),(SELECT count(*) FROM agent_registrations)`).Scan(&accessVersion, &invitations, &registrations)
		if err != nil || accessVersion != 2 || invitations != 1 || registrations != 1 {
			t.Fatalf("preparation changed authority or invitation rows: %v", err)
		}
		current, err := credentials.Identity(ctx, working)
		if err != nil || current != enrolled {
			t.Fatal("preparation changed the existing agent identity")
		}
	}
	raw, tag := call(http.MethodGet, "", "", 200)
	if !strings.HasPrefix(tag, `"`) || !strings.Contains(string(raw), `"required":true`) || !strings.Contains(string(raw), `"required":false`) {
		t.Fatal("missing proof or safe mandatory field projection")
	}
	body := func(protocol string) string {
		raw, _ := json.Marshal(map[string]any{"action": "prepare", "name": "Synthetic router", "values": []map[string]string{
			{"elementId": elementID, "name": "protocol", "source": "\"" + protocol + "\""},
		}})
		return string(raw)
	}
	if worker == "" {
		call(http.MethodPost, tag, body("freedom"), 503)
		assertAuthority()
		return
	}
	call(http.MethodPost, tag, body("synthetic-unknown-protocol"), 422)
	assertAuthority()
	raw, _ = call(http.MethodPost, tag, body("freedom"), 201)
	var result struct{ RouterID, TemplateID, DesiredRevision string }
	if json.Unmarshal(raw, &result) != nil || result.RouterID != routerID || result.TemplateID != templateID || result.DesiredRevision != "1" {
		t.Fatal("invalid receipt for first desired revision")
	}
	assertAuthority()
	desired, err := sqlite.NewPublications(db).ReadDesired(ctx, routerID)
	if err != nil || desired == nil || desired.Number != 1 {
		t.Fatal("first revision was not stored")
	}
	recovered, recoveredTag := call(http.MethodGet, "", "", 200)
	if recoveredTag == tag || !strings.Contains(string(recovered), `"initialDesiredRevision":"1"`) || strings.Contains(string(recovered), `"elements"`) {
		t.Fatal("safe lost-response recovery lacks the exact initial assignment")
	}
	call(http.MethodPost, tag, body("freedom"), 412)
	assertAuthority()
	expired, expiredDigest, err := externalaccess.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE external_access SET protected=1, digest=?, version=3", expiredDigest[:]); err != nil {
		t.Fatal(err)
	}
	bearer = expired
	_, authenticatedTag := call(http.MethodGet, "", "", 200)
	if _, err := db.ExecContext(ctx, "UPDATE external_access SET digest=NULL, version=4"); err != nil {
		t.Fatal(err)
	}
	call(http.MethodGet, "", "", 401)
	call(http.MethodPost, authenticatedTag, body("freedom"), 401)
	after, err := sqlite.NewPublications(db).ReadDesired(ctx, routerID)
	current, identityErr := credentials.Identity(ctx, working)
	if err != nil || after == nil || after.Number != 1 || identityErr != nil || current != enrolled {
		t.Fatal("revoked external authority changed the assignment or existing identity")
	}
	stopAgentProcess(t, p)
}
