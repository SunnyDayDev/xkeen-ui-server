package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func TestProcessExternalRenameChangesOnlyDisplayMetadata(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "server.sqlite")
	invite, keys := fixtureEnrollment(t, path)
	working := agentcredential.Generate(agentcredential.Working).Encode()
	id, err := keys.Enroll(ctx, invite, working)
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
	contacts := sqlite.NewContacts(db, time.Now)
	if err := contacts.Accept(ctx, id, &reporting.Report{Sequence: 4, Revision: 1, State: "applied"}); err != nil {
		t.Fatal(err)
	}
	configuration := func() ([]byte, int64) {
		t.Helper()
		var value []byte
		var generation int64
		if err := db.QueryRowContext(ctx, "SELECT settings,generation FROM active_router_publications WHERE router_id=?", id.RouterID).Scan(&value, &generation); err != nil {
			t.Fatal(err)
		}
		return value, generation
	}
	before, beforeGeneration := configuration()
	cert, key, roots := fixtureServerCertificate(t)
	client := &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{
		DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: roots},
	}}
	defer client.CloseIdleConnections()
	p := startProcess(t, testBinary, "--listen", "127.0.0.1:0", "--db-path", path, "--tls-cert", cert, "--tls-key", key)
	origin := "https://" + p.readyWithClient(t, client, "https")
	resource := origin + "/api/v1/routers/name?" + url.Values{"routerId": {id.RouterID}}.Encode()
	var bearer string
	request := func(method, tag, body string, status int, name string) string {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, resource, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if method == "PATCH" {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", origin)
		}
		if tag != "" {
			req.Header.Set("If-Match", tag)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != status {
			t.Fatalf("router name returned %d, want %d", response.StatusCode, status)
		}
		if status == http.StatusOK {
			var value map[string]string
			if err := json.NewDecoder(response.Body).Decode(&value); err != nil || len(value) != 2 ||
				value["name"] != name || value["routerId"] != id.RouterID || response.Header.Get("ETag") == "" {
				t.Fatal("response did not preserve the exact scope and name")
			}
		}
		return response.Header.Get("ETag")
	}
	old := request("GET", "", "", 200, id.RouterID)
	request("PATCH", old, `{"name":" "}`, 400, "")
	request("PATCH", old, `{"name":null}`, 400, "")
	request("PATCH", "", `{"name":"Лабораторный роутер"}`, 428, "")
	renamed := request("PATCH", old, `{"name":"  Лабораторный роутер  "}`, 200, "  Лабораторный роутер  ")
	request("PATCH", old, `{"name":"другое имя"}`, 412, "")
	if got := request("PATCH", renamed, `{"name":"  Лабораторный роутер  "}`, 200, "  Лабораторный роутер  "); got != renamed {
		t.Fatal("unchanged name advanced metadata generation")
	}
	if err := sqlite.NewRouterVisibility(db).Hide(ctx, id.RouterID); err != nil {
		t.Fatal(err)
	}
	request("GET", "", "", 200, "  Лабораторный роутер  ")
	after, afterGeneration := configuration()
	if !bytes.Equal(before, after) || beforeGeneration != afterGeneration {
		t.Fatal("rename altered published configuration")
	}
	if actual, err := keys.Identity(ctx, working); err != nil || actual != id {
		t.Fatal("rename altered agent identity")
	}
	if status, err := contacts.Status(ctx, id.RouterID); err != nil || status.Desired != 1 || status.Applied != 1 {
		t.Fatal("rename altered revision evidence")
	}
	retired, digest, err := externalaccess.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE external_access SET protected=1, digest=?, version=3", digest[:]); err != nil {
		t.Fatal(err)
	}
	bearer = retired
	valid := request("GET", "", "", 200, "  Лабораторный роутер  ")
	if _, err := db.ExecContext(ctx, "UPDATE external_access SET digest=NULL, version=4"); err != nil {
		t.Fatal(err)
	}
	request("PATCH", valid, `{"name":"не должно сохраниться"}`, 401, "")
	if _, err := db.ExecContext(ctx, "UPDATE external_access SET protected=0, version=5"); err != nil {
		t.Fatal(err)
	}
	bearer = ""
	if restored := request("GET", "", "", 200, "  Лабораторный роутер  "); restored != renamed {
		t.Fatal("revoked request changed display metadata")
	}
	stopAgentProcess(t, p)
}
