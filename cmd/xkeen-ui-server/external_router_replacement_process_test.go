package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestProcessExternalRouterReplacementEditing(t *testing.T) {
	fixture := newExternalRouterFixture(t)
	ctx := t.Context()
	if _, _, err := fixture.access.Owner(ctx, fixture.owner,
		&externalaccess.Change{Version: fixture.settings.Version, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	const values = "/api/v1/routers/variables" + externalRouterSelection
	const replacements = "/api/v1/routers/replacement" + externalRouterSelection
	call := fixture.call
	before := call("GET", values, "", "", "", 200)
	call("PATCH", values, before.Tag, `{"values":[{"name":"port","source":" 17 "}]}`, "", 200)
	permit, err := fixture.access.Admit(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.access.ReadExternalRouterDraft(ctx, permit, "router-example")
	if err != nil || len(snapshot.Editable.Groups) != 1 || snapshot.Editable.Groups[0].Name != "outbounds" {
		t.Fatalf("unexpected test scope: %+v / %v", snapshot.Basis, err)
	}
	exclusion := configuration.ElementSource{TemplateID: snapshot.Basis.TemplateID, ElementID: externalRouterElement}
	snapshot.Editable.Groups[0].Exclusions = []configuration.ElementSource{exclusion}
	if _, err := fixture.access.WriteExternalRouterDraft(ctx, permit, snapshot.Basis, snapshot.Editable); err != nil {
		t.Fatal(err)
	}
	initial := call("GET", replacements, "", "", "", 200)
	if initial.Tag == "" || !bytes.Contains(initial.Body, []byte(`"mode":"variables"`)) ||
		bytes.Contains(initial.Body, []byte(`"personalSource"`)) {
		t.Fatalf("unexpected replacement view: %s", initial.Body)
	}
	for _, request := range []string{
		`{"action":"replace"}`, `{"action":"replace","source":null}`,
		`{"action":"replace","source":"null","confirm":true}`, `{"action":"reset"}`,
		`{"action":"reset","confirm":null}`, `{"action":"variables","confirm":false}`,
		`{"action":"variables","confirm":true,"source":null}`,
	} {
		call("PATCH", replacements, initial.Tag, request, "", 400)
	}
	call("PATCH", replacements, initial.Tag, `{"action":"replace","source":"{"}`, "", 422)
	exact := " {\r\n\"name\":\"$" + "{untouched}\"\n}\r\n"
	raw, err := json.Marshal(map[string]string{"action": "replace", "source": exact})
	if err != nil {
		t.Fatal(err)
	}
	replaced := call("PATCH", replacements, initial.Tag, string(raw), "", 200)
	var body struct{ Source, Mode string }
	if json.Unmarshal(replaced.Body, &body) != nil || body.Source != exact || body.Mode != "replacement" || replaced.Tag == initial.Tag {
		t.Fatalf("replacement not retained exactly: %s", replaced.Body)
	}
	hidden := call("GET", values, "", "", "", 200)
	if bytes.Contains(hidden.Body, []byte(`"personalSource"`)) || bytes.Contains(hidden.Body, []byte(`"untouched"`)) {
		t.Fatal("inactive values or literal escaped the variable representation")
	}
	call("PATCH", replacements, hidden.Tag, `{"action":"variables","confirm":true}`, "", 412)
	call("PATCH", replacements, initial.Tag, `{"action":"variables","confirm":true}`, "", 412)
	returned := call("PATCH", replacements, replaced.Tag, `{"action":"variables","confirm":true}`, "", 200)
	if bytes.Contains(returned.Body, []byte(`"source"`)) {
		t.Fatal("return retained literal JSON")
	}
	if fresh := call("GET", values, "", "", "", 200); !bytes.Contains(fresh.Body, []byte(`"personalSource":" 17 "`)) {
		t.Fatal("return did not restore the preserved personal value")
	}
	again := call("PATCH", replacements, returned.Tag, `{"action":"replace","source":"null"}`, "", 200)
	if !bytes.Contains(again.Body, []byte(`"source":"null"`)) {
		t.Fatalf("literal JSON null was not saved: %s", again.Body)
	}
	reset := call("PATCH", replacements, again.Tag, `{"action":"reset","confirm":true}`, "", 200)
	if !bytes.Contains(reset.Body, []byte(`"hasOverride":false`)) || bytes.Contains(reset.Body, []byte(`"source"`)) {
		t.Fatalf("reset kept an override: %s", reset.Body)
	}
	if after := call("GET", values, "", "", "", 200); bytes.Contains(after.Body, []byte(`"personalSource"`)) {
		t.Fatal("reset retained personal values")
	}
	if wrong := call("PATCH", replacements, reset.Tag, `{"action":"variables","confirm":true}`, "", 409); !bytes.Contains(wrong.Body, []byte(`"code":"incompatible_mode"`)) {
		t.Fatalf("wrong mode result: %s", wrong.Body)
	}
	snapshot, err = fixture.access.ReadExternalRouterDraft(ctx, permit, "router-example")
	if err != nil || len(snapshot.Editable.Groups) != 1 || len(snapshot.Editable.Groups[0].Exclusions) != 1 ||
		snapshot.Editable.Groups[0].Exclusions[0] != exclusion {
		t.Fatalf("reset disturbed the separate exclusion: %+v / %v", snapshot.Editable.Groups, err)
	}
	var desired int64
	if err := fixture.db.QueryRowContext(ctx, "SELECT max(revision) FROM desired_router_revisions").Scan(&desired); err != nil || desired != 1 {
		t.Fatalf("replacement unexpectedly published: %d / %v", desired, err)
	}
}
