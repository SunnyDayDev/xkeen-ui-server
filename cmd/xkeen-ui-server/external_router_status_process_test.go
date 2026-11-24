package main

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func TestProcessExternalRouterStatusKeepsSeparateEvidence(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "server.sqlite")
	invite, credentials := fixtureEnrollment(t, path)
	identity, err := credentials.Enroll(ctx, invite, agentcredential.Generate(agentcredential.Working).Encode())
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	token, digest, err := externalaccess.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE external_access SET enabled=1,protected=1,digest=?,version=2", digest[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO desired_router_revisions SELECT router_id,2,profile,files FROM desired_router_revisions WHERE router_id=?", identity.RouterID); err != nil {
		t.Fatal(err)
	}
	cert, key, roots := fixtureServerCertificate(t)
	client := &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{
		DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: roots},
	}}
	defer client.CloseIdleConnections()
	p := startProcess(t, testBinary, "--listen", "127.0.0.1:0", "--db-path", path, "--tls-cert", cert, "--tls-key", key)
	endpoint := "https://" + p.readyWithClient(t, client, "https")
	rows := func(bearer string, want int) []map[string]string {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/api/v1/routers", nil)
		if err != nil {
			t.Fatal(err)
		}
		if bearer != "" {
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("router list got %d, want %d", response.StatusCode, want)
		}
		if want != http.StatusOK {
			return nil
		}
		var payload struct{ Routers []map[string]string }
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil || payload.Routers == nil {
			t.Fatal("invalid router list envelope")
		}
		return payload.Routers
	}
	rows("", http.StatusUnauthorized)
	if items := rows(token, 200); len(items) != 0 {
		t.Fatal("an unobserved device was listed")
	}
	now := time.Now().Add(-4 * time.Minute)
	contacts := sqlite.NewContacts(db, func() time.Time { return now })
	if err := contacts.Accept(ctx, identity, &reporting.Report{Sequence: 1, Revision: 1, State: "applied"}); err != nil {
		t.Fatal(err)
	}
	if err := contacts.Accept(ctx, identity, &reporting.Report{Sequence: 2, Revision: 2, State: "failed", Stage: "rollback", Code: "rollback_failed"}); err != nil {
		t.Fatal(err)
	}
	items := rows(token, 200)
	if len(items) != 1 || len(items[0]) != 8 || items[0]["routerId"] != identity.RouterID || items[0]["templateId"] != "template-example" ||
		items[0]["name"] != identity.RouterID || items[0]["contact"] != "offline" || items[0]["appliedRevision"] != "1" ||
		items[0]["desiredRevision"] != "2" || items[0]["runtime"] != "unknown" || items[0]["recovery"] != "rollback_failed" {
		t.Fatalf("no separated offline evidence: %+v", items)
	}
	if err := sqlite.NewRouterVisibility(db).Hide(ctx, identity.RouterID); err != nil {
		t.Fatal(err)
	}
	if items := rows(token, 200); len(items) != 0 {
		t.Fatal("hidden router was returned")
	}
	now = time.Now()
	if err := contacts.Accept(ctx, identity, nil); err != nil {
		t.Fatal(err)
	}
	items = rows(token, 200)
	if len(items) != 1 || items[0]["contact"] != "online" || items[0]["appliedRevision"] != "1" {
		t.Fatal("accepted heartbeat lost separate historical evidence")
	}
	if _, err := db.ExecContext(ctx, "UPDATE external_access SET protected=0,version=3"); err != nil {
		t.Fatal(err)
	}
	if items := rows("", 200); len(items) != 1 {
		t.Fatal("explicitly open F20 mode did not read the same view")
	}
	stopAgentProcess(t, p)
}
