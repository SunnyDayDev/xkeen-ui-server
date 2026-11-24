package main

import (
	"bytes"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

const externalRouterElement = "550e8400-e29b-41d4-a716-446655440000"
const externalRouterSelection = "?routerId=router-example&elementId=" + externalRouterElement

type externalRouterResponse struct {
	Body []byte
	Tag  string
}

type externalRouterFixture struct {
	db       *sql.DB
	owner    [32]byte
	settings externalaccess.Settings
	access   *sqlite.ExternalAccess
	call     func(method, route, tag, body, bearer string, want int) externalRouterResponse
}

func newExternalRouterFixture(t *testing.T) externalRouterFixture {
	t.Helper()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "server.sqlite")
	fixtureEnrollment(t, path)
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const accepted = `[{"name":"outbounds","elements":[{"disabled":false,"record":{"id":"` + externalRouterElement + `","content":"{\"port\":\"$` + `{port}\"}","variables":[{"name":"port","type":"number","default":443}]}}]}]`
	if _, err := db.ExecContext(ctx, "UPDATE accepted_templates SET draft=?", []byte(accepted)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO agent_contacts(router_id,contact_ns,report,applied,uncertain)
		VALUES('router-example',?,NULL,0,0)`, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}

	admin := sqlite.NewWebAdmin(db)
	owner := [32]byte{9}
	if err := admin.SaveVerifier(ctx, webadmin.Verifier{Version: 1}, false); err != nil {
		t.Fatal(err)
	}
	if err := admin.Issue(ctx, 1, owner, [32]byte{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	access := sqlite.NewExternalAccess(db, time.Now)
	settings, _, err := access.Owner(ctx, owner, nil)
	if err != nil {
		t.Fatal(err)
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
	process := startProcess(t, testBinary, "--listen", listen, "--db-path", path, "--tls-cert", cert, "--tls-key", key)
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	t.Cleanup(client.CloseIdleConnections)
	process.readyWithClient(t, client, "https")
	call := func(method, route, tag, body, bearer string, want int) externalRouterResponse {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, "https://"+listen+route, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if tag != "" {
			req.Header.Set("If-Match", tag)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != want || !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
			t.Fatalf("%s %s: got %d want %d (%s; %v)", method, route, res.StatusCode, want, data, err)
		}
		return externalRouterResponse{data, res.Header.Get("ETag")}
	}
	return externalRouterFixture{db: db, owner: owner, settings: settings, access: access, call: call}
}

func TestProcessExternalRouterVariableEditing(t *testing.T) {
	fixture := newExternalRouterFixture(t)
	ctx := t.Context()
	call, db, owner, settings, access := fixture.call, fixture.db, fixture.owner, fixture.settings, fixture.access
	var err error
	const pathToValues = "/api/v1/routers/variables" + externalRouterSelection
	const element = externalRouterElement
	const update = `{"values":[{"name":"port","source":" 9007199254740993 "}]}`
	call(http.MethodGet, pathToValues, "", "", "", 404)
	settings, _, err = access.Owner(ctx, owner, &externalaccess.Change{Version: settings.Version, Enabled: true, Protected: false})
	if err != nil {
		t.Fatal(err)
	}
	first := call(http.MethodGet, pathToValues, "", "", "", 200)
	if first.Tag == "" || !bytes.Contains(first.Body, []byte(`"defaultSource":"443"`)) ||
		bytes.Contains(first.Body, []byte(`"sections"`)) {
		t.Fatalf("wrong initial external view: %s", first.Body)
	}
	call(http.MethodPatch, pathToValues, "", update, "", 428)
	call(http.MethodPatch, pathToValues, first.Tag, `{"values":[{"name":"port","source":null}]}`, "", 400)
	call(http.MethodPatch, pathToValues, first.Tag, `{"values":[{"name":"port","source":"{"}]}`, "", 422)
	saved := call(http.MethodPatch, pathToValues, first.Tag, update, "", 200)
	if saved.Tag == "" || saved.Tag == first.Tag || !bytes.Contains(saved.Body, []byte(`"personalSource":" 9007199254740993 "`)) {
		t.Fatalf("literal source lost or precondition unchanged: %s", saved.Body)
	}
	call(http.MethodPatch, pathToValues, first.Tag, update, "", 412)
	if again := call(http.MethodGet, pathToValues, "", "", "", 200); again.Tag != saved.Tag || !bytes.Equal(again.Body, saved.Body) {
		t.Fatal("conditional result differs from the accepted draft")
	}
	var mismatched struct {
		Definitions []struct{ Issue string }
	}
	literalNull := call(http.MethodPatch, pathToValues, saved.Tag, `{"values":[{"name":"port","source":"null"}]}`, "", 200)
	if json.Unmarshal(literalNull.Body, &mismatched) != nil || len(mismatched.Definitions) != 1 || mismatched.Definitions[0].Issue == "" {
		t.Fatalf("not-ready literal lost its B01 diagnostic: %s", literalNull.Body)
	}
	inherited := call(http.MethodPatch, pathToValues, literalNull.Tag, `{"values":[{"name":"port"}]}`, "", 200)
	if bytes.Contains(inherited.Body, []byte(`"personalSource"`)) || !bytes.Contains(inherited.Body, []byte(`"defaultSource":"443"`)) {
		t.Fatalf("explicit inheritance lost the definition or retained the override: %s", inherited.Body)
	}
	permit, err := access.Admit(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := access.ReadExternalRouterDraft(ctx, permit, "router-example")
	if err != nil {
		t.Fatal(err)
	}
	base := configuration.ElementSource{TemplateID: snapshot.Basis.TemplateID, ElementID: element}
	literal, err := routersettings.EditReplacement(snapshot.Editable, "outbounds", base,
		configuration.ContentAction{Kind: configuration.ActivateReplacement, Content: []byte(`{"my":"literal"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := access.WriteExternalRouterDraft(ctx, permit, snapshot.Basis, literal); err != nil {
		t.Fatal(err)
	}
	replaced := call(http.MethodGet, pathToValues, "", "", "", 200)
	if replaced.Tag == inherited.Tag || !bytes.Contains(replaced.Body, []byte(`"mode":"replacement"`)) ||
		!bytes.Contains(replaced.Body, []byte(`"definitions":[]`)) ||
		bytes.Contains(replaced.Body, []byte(`"my"`)) {
		t.Fatalf("replacement-only data leaked or mode/version did not change: %s", replaced.Body)
	}
	call(http.MethodPatch, pathToValues, replaced.Tag, update, "", 409)
	if unchanged := call(http.MethodGet, pathToValues, "", "", "", 200); unchanged.Tag != replaced.Tag {
		t.Fatal("declined variable change modified the replacement")
	}

	settings, _, err = access.Owner(ctx, owner, &externalaccess.Change{Version: settings.Version, Enabled: true, Protected: true})
	if err != nil {
		t.Fatal(err)
	}
	call(http.MethodGet, pathToValues, "", "", "", 401)
	settings, bearer, err := access.Owner(ctx, owner, &externalaccess.Change{Version: settings.Version, Rotate: true})
	if err != nil {
		t.Fatal(err)
	}
	call(http.MethodGet, pathToValues, "", "", bearer, 200)
	if _, _, err := access.Owner(ctx, owner, &externalaccess.Change{Version: settings.Version, Rotate: true}); err != nil {
		t.Fatal(err)
	}
	call(http.MethodPatch, pathToValues, inherited.Tag, update, bearer, 401)
	var revision int64
	if err := db.QueryRowContext(ctx, "SELECT max(revision) FROM desired_router_revisions").Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("external draft editor changed desired revision: %d / %v", revision, err)
	}
}
