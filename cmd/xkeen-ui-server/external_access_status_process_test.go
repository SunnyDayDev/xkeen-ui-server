package main

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func TestProcessExternalAccessReadsOnlyAdmittedMode(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "server.sqlite")
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	admin := sqlite.NewWebAdmin(db)
	if err := admin.SaveVerifier(ctx, webadmin.Verifier{Version: 1, Salt: [16]byte{1}, Digest: [32]byte{1}}, false); err != nil {
		t.Fatal(err)
	}
	session := [32]byte{1}
	if err := admin.Issue(ctx, 1, session, [32]byte{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	settings := sqlite.NewExternalAccess(db, time.Now)
	change := func(v externalaccess.Change) (externalaccess.Settings, string) {
		t.Helper()
		value, token, err := settings.Owner(ctx, session, &v)
		if err != nil {
			t.Fatal(err)
		}
		return value, token
	}
	cert, key, roots := fixtureServerCertificate(t)
	client := &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{
		DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: roots},
	}}
	defer client.CloseIdleConnections()
	p := startProcess(t, testBinary, "--listen", "127.0.0.1:0", "--db-path", path, "--tls-cert", cert, "--tls-key", key)
	origin := "https://" + p.readyWithClient(t, client, "https")
	request := func(method, suffix, token string, want int, protected bool) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, origin+"/api/v1/access"+suffix, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if method != http.MethodGet {
			req.Header.Set("Origin", origin)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("access returned %d, want %d", response.StatusCode, want)
		}
		if want == http.StatusOK {
			var value map[string]bool
			if err := json.NewDecoder(response.Body).Decode(&value); err != nil ||
				!reflect.DeepEqual(value, map[string]bool{"enabled": true, "protected": protected}) {
				t.Fatal("response contains unexpected mode fields")
			}
		}
	}
	request("GET", "", "", 404, false)
	active, _ := change(externalaccess.Change{Version: 1, Enabled: true})
	request("GET", "", "", 200, false)
	request("GET", "?version=1", "", 400, false)
	request("POST", "", "", 405, false)
	request("GET", "", "", 200, false)
	active, _ = change(externalaccess.Change{Version: active.Version, Enabled: true, Protected: true})
	request("GET", "", "", 401, false)
	active, oldToken := change(externalaccess.Change{Version: active.Version, Rotate: true})
	request("GET", "", oldToken, 200, true)
	active, nextToken := change(externalaccess.Change{Version: active.Version, Rotate: true})
	request("GET", "", oldToken, 401, false)
	request("GET", "", nextToken, 200, true)
	change(externalaccess.Change{Version: active.Version, Protected: true})
	request("GET", "", nextToken, 404, false)
	stopAgentProcess(t, p)
}
