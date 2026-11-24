package main

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func TestProcessExternalHidePreservesIdentityAndRejectsReusedETag(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "server.sqlite")
	invite, credentials := fixtureEnrollment(t, path)
	working := agentcredential.Generate(agentcredential.Working).Encode()
	id, err := credentials.Enroll(ctx, invite, working)
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
	cert, key, roots := fixtureServerCertificate(t)
	client := &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{
		DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: roots},
	}}
	defer client.CloseIdleConnections()
	p := startProcess(t, testBinary, "--listen", "127.0.0.1:0", "--db-path", path, "--tls-cert", cert, "--tls-key", key)
	origin := "https://" + p.readyWithClient(t, client, "https")
	resource := origin + "/api/v1/routers/visibility?" + url.Values{"routerId": {id.RouterID}}.Encode()
	request := func(method, tag, body string, want int, visible bool) string {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, resource, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if method == "POST" {
			req.Header.Set("Origin", origin)
			req.Header.Set("Content-Type", "application/json")
		}
		if tag != "" {
			req.Header.Set("If-Match", tag)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("visibility returned %d, want %d", response.StatusCode, want)
		}
		if want == http.StatusOK {
			var value struct {
				RouterID string `json:"routerId"`
				Visible  bool   `json:"visible"`
			}
			if err := json.NewDecoder(response.Body).Decode(&value); err != nil ||
				value.RouterID != id.RouterID || value.Visible != visible || response.Header.Get("ETag") == "" {
				t.Fatal("invalid visibility response")
			}
		}
		return response.Header.Get("ETag")
	}
	request("GET", "", "", 404, false)
	instant := time.Now()
	contact := sqlite.NewContacts(db, func() time.Time { return instant })
	if err := contact.Accept(ctx, id, &reporting.Report{Sequence: 4, Revision: 1, State: "applied"}); err != nil {
		t.Fatal(err)
	}
	initial := request("GET", "", "", 200, true)
	request("POST", "", `{"action":"hide","confirm":true}`, 428, false)
	request("POST", initial, `{"action":"hide"}`, 400, false)
	hidden := request("POST", initial, `{"action":"hide","confirm":true}`, 200, false)
	request("POST", initial, `{"action":"hide","confirm":true}`, 412, false)
	if repeated := request("POST", hidden, `{"action":"hide","confirm":true}`, 200, false); repeated != hidden {
		t.Fatal("exact repeated hide changed the generation")
	}
	if err := contact.Accept(ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	revived := request("GET", "", "", 200, true)
	if revived == initial {
		t.Fatal("visibility cycle revived a stale ETag at an identical contact timestamp")
	}
	request("POST", initial, `{"action":"hide","confirm":true}`, 412, false)
	if got, err := credentials.Identity(ctx, working); err != nil || got != id {
		t.Fatal("hide modified the working identity")
	}
	if status, err := contact.Status(ctx, id.RouterID); err != nil || status.Applied != 1 || status.Desired != 1 {
		t.Fatal("hide modified publication or applied state")
	}
	stopAgentProcess(t, p)
}
