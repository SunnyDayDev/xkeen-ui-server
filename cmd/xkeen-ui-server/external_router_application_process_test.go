package main

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func TestProcessExternalApplicationRequiresExactEvidence(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "server.sqlite")
	invite, keys := fixtureEnrollment(t, path)
	identity, err := keys.Enroll(ctx, invite, agentcredential.Generate(agentcredential.Working).Encode())
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "UPDATE external_access SET enabled=1,protected=0,version=2"); err != nil {
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
	read := func(revision string, want int) map[string]string {
		t.Helper()
		query := url.Values{"routerId": {identity.RouterID}, "revision": {revision}}
		response, err := client.Get(endpoint + "/api/v1/routers/application?" + query.Encode())
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("revision %s returned %d, want %d", revision, response.StatusCode, want)
		}
		if want != 200 {
			return nil
		}
		var status map[string]string
		if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
			t.Fatal(err)
		}
		if status["routerId"] != identity.RouterID || status["revision"] != revision || status["latestDesiredRevision"] != "2" ||
			status["sequence"] != "" || status["files"] != "" {
			t.Fatal("incorrect scope or unsafe status fields")
		}
		return status
	}
	read("01", 400)
	read("3", 404)
	if first := read("1", 200); first["contact"] != "unobserved" || first["result"] != "unknown" {
		t.Fatalf("invented unobserved evidence: %+v", first)
	}
	contacts := sqlite.NewContacts(db, time.Now)
	report := func(value reporting.Report) {
		t.Helper()
		if err := contacts.Accept(ctx, identity, &value); err != nil {
			t.Fatal(err)
		}
	}
	report(reporting.Report{Sequence: 1, Revision: 1, State: "applied"})
	if state := read("1", 200); state["result"] != "applied" || state["runtime"] != "verified" {
		t.Fatalf("exact proof missing: %+v", state)
	}
	if state := read("2", 200); state["result"] != "unknown" || state["historicallyAppliedRevision"] != "1" {
		t.Fatalf("desired number invented a successful result: %+v", state)
	}
	report(reporting.Report{Sequence: 2, Revision: 1, State: "failed", Stage: "rollback", Code: "rollback_failed"})
	if state := read("1", 200); state["result"] != "failed" || state["historicallyAppliedRevision"] != "1" ||
		state["runtime"] != "unknown" || state["stage"] != "rollback" || state["code"] != "rollback_failed" {
		t.Fatalf("historical success masked latest failure: %+v", state)
	}
	report(reporting.Report{Sequence: 3, Revision: 2, State: "applied"})
	if state := read("1", 200); state["result"] != "unknown" || state["historicallyAppliedRevision"] != "2" || state["code"] != "" {
		t.Fatalf("invented history for a superseded revision: %+v", state)
	}
	if err := sqlite.NewRouterVisibility(db).Hide(ctx, identity.RouterID); err != nil {
		t.Fatal(err)
	}
	if state := read("2", 200); state["result"] != "applied" {
		t.Fatal("exact F20 scope unexpectedly depended on list visibility")
	}
	if _, err := db.ExecContext(ctx, "UPDATE external_access SET enabled=0,version=3"); err != nil {
		t.Fatal(err)
	}
	read("2", 404)
	stopAgentProcess(t, p)
}
