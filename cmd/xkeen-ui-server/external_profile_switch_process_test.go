package main

import (
	"bytes"
	"encoding/json"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProcessExternalProfileSwitchCurrent(t *testing.T) {
	f := newExternalRouterFixture(t)
	const route = "/api/v1/routers/profile-switch?routerId=router-example&templateId=template-example"
	f.call("GET", route, "", "", "", 404)
	_, _, err := f.access.Owner(t.Context(), f.owner, &externalaccess.Change{Version: f.settings.Version, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	got := f.call("GET", route, "", "", "", 200)
	if !bytes.Contains(got.Body, []byte(`"alreadyActive":true`)) || bytes.Contains(got.Body, []byte("443")) {
		t.Fatal(string(got.Body))
	}
	f.call("POST", route, "", `{"action":"preview","values":[]}`, "", 409)
	for _, body := range []string{`{"action":"preview"}`, `{"action":"preview","values":null}`, `{"action":"preview","values":[],"receipt":null}`, `{"action":"preview","values":[],"receipt":""}`} {
		f.call("POST", route, "", body, "", 400)
	}
	f.call("POST", route, "", `{"action":"confirm"}`, "", 400)
}

func TestProcessExternalProfileSwitchConfirmation(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "xkeen-switch-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "worker")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := os.Chmod(socket, 0600); err != nil {
		t.Fatal(err)
	}
	var reject atomic.Bool
	var beforeReply atomic.Pointer[func()]
	worker := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Attempt, Profile, Digest string
			Files                    []struct {
				Name  string
				Bytes []byte
			}
		}
		if r.URL.Path != "/v1/validate" || json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Files) < 5 {
			http.Error(w, "invalid fixture request", 400)
			return
		}
		outcome := "ok"
		if reject.Load() {
			outcome = "rejected"
		}
		if hook := beforeReply.Swap(nil); hook != nil {
			(*hook)()
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"Attempt": input.Attempt, "Profile": input.Profile, "Digest": input.Digest, "Outcome": outcome})
	})}
	go worker.Serve(l)
	defer worker.Close()
	f := newExternalRouterFixture(t, "--validator-socket", socket)
	ctx := t.Context()
	settings, _, err := f.access.Owner(ctx, f.owner, &externalaccess.Change{Version: f.settings.Version, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	const sections = `[{"name":"log","elements":[]},{"name":"api","elements":[]},{"name":"inbounds","elements":[]},{"name":"outbounds","elements":[]},{"name":"routing","elements":[]},{"name":"rules","elements":[]}]`
	if _, err = f.db.ExecContext(ctx, "UPDATE accepted_templates SET draft=?", []byte(sections)); err != nil {
		t.Fatal(err)
	}
	const canary = "synthetic-private-office"
	office := strings.Replace(sections, `"name":"outbounds","elements":[]`, `"name":"outbounds","elements":[{"disabled":false,"record":{"id":"`+externalRouterElement+`","content":"{\"protocol\":\"freedom\",\"tag\":\"`+"$"+`{label}\"}","variables":[{"name":"label","type":"string","default":"`+canary+`"}]}}]`, 1)
	if _, err = f.db.ExecContext(ctx, "INSERT INTO accepted_templates(template_id,version,draft) VALUES ('office',1,?)", []byte(office)); err != nil {
		t.Fatal(err)
	}
	const root = "/api/v1/routers/profile-switch?routerId=router-example&templateId="
	get := f.call("GET", root+"office", "", "", "", 200)
	if bytes.Contains(get.Body, []byte(canary)) || !bytes.Contains(get.Body, []byte(`"label"`)) {
		t.Fatal("unsafe or incomplete field projection")
	}
	required := strings.Replace(office, `,"default":"`+canary+`"`, "", 1)
	if _, err = f.db.Exec("INSERT INTO accepted_templates(template_id,version,draft) VALUES ('required',1,?)", []byte(required)); err != nil {
		t.Fatal(err)
	}
	fields := f.call("GET", root+"required", "", "", "", 200)
	if !bytes.Contains(fields.Body, []byte(`"required":true`)) {
		t.Fatal("missing mandatory field")
	}
	f.call("POST", root+"required", "", `{"action":"preview","values":[]}`, "", 422)
	preview := func(target string) string {
		response := f.call("POST", root+target, "", `{"action":"preview","values":[]}`, "", 200)
		var doc struct {
			Receipt string
			Order   struct{ State string }
		}
		if json.Unmarshal(response.Body, &doc) != nil || doc.Receipt == "" || doc.Order.State != "inherit" {
			t.Fatal("missing receipt")
		}
		return doc.Receipt
	}
	confirm := func(target, receipt string, status int) {
		f.call("POST", root+target, "", `{"action":"confirm","receipt":"`+receipt+`","values":[]}`, "", status)
	}
	reject.Store(true)
	f.call("POST", root+"office", "", `{"action":"preview","values":[]}`, "", 422)
	reject.Store(false)
	ticket := preview("office")
	confirm("template-example", ticket, 409)
	f.call("POST", root+"office", "", `{"action":"confirm","receipt":"`+ticket+`","values":[{"elementId":"`+externalRouterElement+`","name":"label","source":"\"changed\""}]}`, "", 409)
	confirm("office", ticket, 200)
	confirm("office", ticket, 409)
	if got := f.call("GET", root+"office", "", "", "", 200); !bytes.Contains(got.Body, []byte(`"alreadyActive":true`)) {
		t.Fatal("lost reply could not be resolved")
	}
	back := preview("template-example")
	confirm("template-example", back, 200)
	const personalCanary = "synthetic-personal-label"
	const edits = `"values":[{"elementId":"` + externalRouterElement + `","name":"label","source":"\"synthetic-personal-label\""}]`
	edited := f.call("POST", root+"office", "", `{"action":"preview",`+edits+`}`, "", 200)
	var selected struct{ Receipt string }
	if json.Unmarshal(edited.Body, &selected) != nil || selected.Receipt == "" || bytes.Contains(edited.Body, []byte(personalCanary)) {
		t.Fatal("unsafe personal preview")
	}
	f.call("POST", root+"office", "", `{"action":"confirm","receipt":"`+selected.Receipt+`",`+edits+`}`, "", 200)
	confirm("template-example", preview("template-example"), 200)
	restored := f.call("GET", root+"office", "", "", "", 200)
	if !bytes.Contains(restored.Body, []byte(`"hasPersonalValue":true`)) || bytes.Contains(restored.Body, []byte(personalCanary)) {
		t.Fatal("lost or exposed saved value")
	}
	// A replaced shared UUID leaves the saved override intact and blocks a new preparation.
	oldID := externalRouterElement
	stale := strings.ReplaceAll(office, oldID, "550e8400-e29b-41d4-a716-446655440099")
	if _, err = f.db.Exec("INSERT INTO accepted_templates(template_id,version,draft) VALUES ('office',2,?)", []byte(stale)); err != nil {
		t.Fatal(err)
	}
	f.call("POST", root+"office", "", `{"action":"preview","values":[]}`, "", 422)
	if _, err = f.db.Exec("INSERT INTO accepted_templates(template_id,version,draft) VALUES ('office',3,?)", []byte(office)); err != nil {
		t.Fatal(err)
	}
	ticket = preview("office")
	// Rotation invalidates an old permit and a receipt from the previous policy,
	// even when the current caller has the newly issued bearer.
	settings, _, err = f.access.Owner(ctx, f.owner, &externalaccess.Change{Version: settings.Version, Enabled: true, Protected: true})
	if err != nil {
		t.Fatal(err)
	}
	settings, bearer, err := f.access.Owner(ctx, f.owner, &externalaccess.Change{Version: settings.Version, Rotate: true})
	if err != nil {
		t.Fatal(err)
	}
	f.call("GET", root+"office", "", "", "", 401)
	f.call("POST", root+"office", "", `{"action":"confirm","values":[],"receipt":"`+ticket+`"}`, bearer, 409)
	checked := f.call("POST", root+"office", "", `{"action":"preview","values":[]}`, bearer, 200)
	var renewed struct{ Receipt string }
	if json.Unmarshal(checked.Body, &renewed) != nil || renewed.Receipt == "" {
		t.Fatal("missing protected preview")
	}
	ticket = renewed.Receipt
	disable := func() {
		_, _, err := f.access.Owner(ctx, f.owner, &externalaccess.Change{Version: settings.Version})
		if err != nil {
			t.Error(err)
		}
	}
	beforeReply.Store(&disable)
	f.call("POST", root+"office", "", `{"action":"confirm","receipt":"`+ticket+`","values":[]}`, bearer, 404)
	var active string
	if err := f.db.QueryRow("SELECT template_id FROM active_router_publications WHERE router_id='router-example'").Scan(&active); err != nil || active != "template-example" {
		t.Fatal("late revoke changed assignment", active, err)
	}
}
