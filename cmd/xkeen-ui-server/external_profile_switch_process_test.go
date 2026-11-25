package main

import (
	"bytes"
	"encoding/json"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	f.call("POST", route, "", `{"action":"preview"}`, "", 409)
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
	if _, err = f.db.ExecContext(ctx, "INSERT INTO accepted_templates(template_id,version,draft) VALUES ('office',1,?)", []byte(sections)); err != nil {
		t.Fatal(err)
	}
	const root = "/api/v1/routers/profile-switch?routerId=router-example&templateId="
	preview := func(target string) string {
		response := f.call("POST", root+target, "", `{"action":"preview"}`, "", 200)
		var doc struct{ Receipt string }
		if json.Unmarshal(response.Body, &doc) != nil || doc.Receipt == "" {
			t.Fatal("missing receipt")
		}
		return doc.Receipt
	}
	confirm := func(target, receipt string, status int) {
		f.call("POST", root+target, "", `{"action":"confirm","receipt":"`+receipt+`"}`, "", status)
	}
	reject.Store(true)
	f.call("POST", root+"office", "", `{"action":"preview"}`, "", 422)
	reject.Store(false)
	ticket := preview("office")
	confirm("template-example", ticket, 409)
	confirm("office", ticket, 200)
	confirm("office", ticket, 409)
	if got := f.call("GET", root+"office", "", "", "", 200); !bytes.Contains(got.Body, []byte(`"alreadyActive":true`)) {
		t.Fatal("lost reply could not be resolved")
	}
	back := preview("template-example")
	confirm("template-example", back, 200)
	ticket = preview("office")
	disable := func() {
		_, _, err := f.access.Owner(ctx, f.owner, &externalaccess.Change{Version: settings.Version})
		if err != nil {
			t.Error(err)
		}
	}
	beforeReply.Store(&disable)
	f.call("POST", root+"office", "", `{"action":"confirm","receipt":"`+ticket+`"}`, "", 404)
	var active string
	if err := f.db.QueryRow("SELECT template_id FROM active_router_publications WHERE router_id='router-example'").Scan(&active); err != nil || active != "template-example" {
		t.Fatal("late revoke changed assignment", active, err)
	}
}
