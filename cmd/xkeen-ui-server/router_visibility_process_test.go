package main

import (
	"crypto/tls"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func TestProcessRouterVisibilityReturnsOnlyOnSuccessfulHTTPSContact(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "server.sqlite")
	invitation, keys := fixtureEnrollment(t, path)
	key := agentcredential.Generate(agentcredential.Working).Encode()
	identity, err := keys.Enroll(ctx, invitation, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, private, roots := fixtureServerCertificate(t)
	process := startProcess(t, testBinary, "--db-path", path, "--listen", "127.0.0.1:0", "--tls-cert", cert, "--tls-key", private)
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	defer client.CloseIdleConnections()
	origin := "https://" + process.readyWithClient(t, client, "https")
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	view := routersettings.NewVisibility(sqlite.NewRouterVisibility(db))
	send := func(method, path, secret, body string, want int) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, origin+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+secret)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		reply, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer reply.Body.Close()
		if reply.StatusCode != want {
			t.Fatalf("%s %s: %d want %d", method, path, reply.StatusCode, want)
		}
	}
	projection := func(want bool) {
		t.Helper()
		ids, err := view.VisibleIDs(ctx)
		if err != nil || (want && (len(ids) != 1 || ids[0] != identity.RouterID)) || (!want && len(ids) != 0) {
			t.Fatalf("unexpected projection %q: %v", ids, err)
		}
	}
	const contact = "/agent/v1/contact"
	send(http.MethodPost, contact, key, "", 204)
	projection(true)
	if err := view.Hide(ctx, identity.RouterID); err != nil {
		t.Fatal(err)
	}
	projection(false)
	send(http.MethodGet, "/agent/v1/identity", key, "", 200)
	send(http.MethodGet, contact, key, "", 200)
	send(http.MethodGet, "/agent/v1/desired", key, "", 200)
	send(http.MethodPost, contact, key, `{"report":null}`, 400)
	send(http.MethodPost, contact, agentcredential.Generate(agentcredential.Working).Encode(), "", 401)
	projection(false)
	send(http.MethodPost, contact, key, "", 204)
	projection(true)
}
