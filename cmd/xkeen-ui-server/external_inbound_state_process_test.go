package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestProcessExternalInboundState(t *testing.T) {
	f := newExternalRuleFixture(t)
	fixtureExternalLocalIO(t, f)
	ctx := t.Context()
	p, err := f.access.Admit(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	const (
		sharedDisabled = "550e8400-e29b-41d4-a716-446655440081"
		sharedExcluded = "550e8400-e29b-41d4-a716-446655440082"
		localDuplicate = "550e8400-e29b-41d4-a716-446655440083"
	)
	const inbounds = `{"name":"inbounds","elements":[
		{"disabled":true,"record":{"id":"` + sharedDisabled + `","content":"null","variables":[]}},
		{"disabled":false,"record":{"id":"` + sharedExcluded + `","content":"null","variables":[]}}]}`
	if _, err := f.db.ExecContext(ctx, `UPDATE accepted_templates
		SET draft=CAST(json_insert(CAST(draft AS TEXT), '$[#]', json(?)) AS BLOB)`, inbounds); err != nil {
		t.Fatal(err)
	}
	snap, err := f.access.ReadExternalRouterDraft(ctx, p, "router-example")
	if err != nil {
		t.Fatal(err)
	}
	for i := range snap.Editable.Groups {
		group := &snap.Editable.Groups[i]
		if group.Name == "inbounds" {
			group.Local = append(group.Local, routersettings.LocalElement{ID: localDuplicate, Content: json.RawMessage(`{"tag":"private-inbound"}`)})
			group.Exclusions = []configuration.ElementSource{{TemplateID: snap.Basis.TemplateID, ElementID: sharedExcluded}}
		}
	}
	snap.Editable.DisabledInbounds = []string{sharedDisabled, sharedExcluded, localDuplicate}
	if _, err := f.access.WriteExternalRouterDraft(ctx, p, snap.Basis, snap.Editable); err != nil {
		t.Fatal(err)
	}

	const endpoint = "/api/v1/routers/inbounds?routerId=router-example&elementId="
	read := func(id string) externalRouterResponse { return f.call("GET", endpoint+id, "", "", "", 200) }
	patch := func(id, tag, action string, status int) externalRouterResponse {
		return f.call("PATCH", endpoint+id, tag, `{"action":"`+action+`"}`, "", status)
	}
	check := func(r externalRouterResponse, personal, disabled, excluded, active bool) {
		t.Helper()
		var view struct{ PersonalDisabled, TemplateDisabled, Excluded, Active bool }
		if json.Unmarshal(r.Body, &view) != nil || view.PersonalDisabled != personal || view.TemplateDisabled != disabled ||
			view.Excluded != excluded || view.Active != active || r.Tag == "" || bytes.Contains(r.Body, []byte("private-inbound")) {
			t.Fatalf("wrong state: %s", r.Body)
		}
	}
	before := read(sharedDisabled)
	check(before, true, true, false, false)
	check(patch(sharedDisabled, before.Tag, "enable", 200), false, true, false, false)
	patch(sharedDisabled, before.Tag, "enable", 412)
	before = read(sharedExcluded)
	check(before, true, false, true, false)
	check(patch(sharedExcluded, before.Tag, "enable", 200), false, false, true, false)
	invalid := read(localDuplicate)
	patch(localDuplicate, invalid.Tag, "enable", 409)
	if read(localDuplicate).Tag != invalid.Tag {
		t.Fatal("invalid enable was written")
	}

	before = read(externalLocalInbound)
	off := patch(externalLocalInbound, before.Tag, "disable", 200)
	check(off, true, false, false, false)
	check(patch(externalLocalInbound, off.Tag, "enable", 200), false, false, false, true)
	base := f.call("GET", externalRulesURL, "", "", "", 200)
	f.call("POST", externalRulesURL, base.Tag, `{"afterId":"","source":"{\"inboundTag\":[\"private-inbound\"]}"}`, "", 201)
	before = read(externalLocalInbound)
	result := patch(externalLocalInbound, before.Tag, "disable", 409)
	if !bytes.Contains(result.Body, []byte("inbound_in_use")) || bytes.Contains(result.Body, []byte("private-inbound")) || read(externalLocalInbound).Tag != before.Tag {
		t.Fatal("blocked disable leaked or changed data", string(result.Body))
	}
}
