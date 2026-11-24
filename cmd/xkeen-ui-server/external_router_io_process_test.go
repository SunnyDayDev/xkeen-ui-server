package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

func TestProcessExternalEffectiveRouterIO(t *testing.T) {
	f := newExternalRouterFixture(t)
	ctx := t.Context()
	const endpoint = "/api/v1/routers/io?routerId=router-example"
	f.call("GET", endpoint, "", "", "", 404)
	policy, _, err := f.access.Owner(ctx, f.owner, &externalaccess.Change{Version: f.settings.Version, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	first := f.call("GET", endpoint, "", "", "", 200)
	if !bytes.Contains(first.Body, []byte(`"inbounds":[]`)) || !bytes.Contains(first.Body, []byte(externalRouterElement)) {
		t.Fatalf("wrong effective inventory: %s", first.Body)
	}
	p, err := f.access.Admit(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := f.access.ReadExternalRouterDraft(ctx, p, "router-example")
	if err != nil {
		t.Fatal(err)
	}
	const local = "550e8400-e29b-41d4-a716-446655440001"
	const disabled = "550e8400-e29b-41d4-a716-446655440002"
	snap.Editable.Groups = append(snap.Editable.Groups, routersettings.Group{
		Name: "inbounds", Local: []routersettings.LocalElement{
			{ID: local, Content: json.RawMessage(`{"tag":"synthetic-private-tag"}`)},
			{ID: disabled, Content: json.RawMessage(`null`)},
		},
	})
	snap.Editable.DisabledInbounds = []string{disabled}
	if _, err := f.access.WriteExternalRouterDraft(ctx, p, snap.Basis, snap.Editable); err != nil {
		t.Fatal(err)
	}
	view := f.call("GET", endpoint, "", "", "", 200)
	for _, forbidden := range []string{disabled, "synthetic-private-tag", "443", "source"} {
		if bytes.Contains(view.Body, []byte(forbidden)) {
			t.Fatalf("inventory leaked %q: %s", forbidden, view.Body)
		}
	}
	var reply struct {
		Inbounds, Outbounds []struct{ ID, Origin string }
	}
	if json.Unmarshal(view.Body, &reply) != nil || len(reply.Inbounds) != 1 || reply.Inbounds[0].ID != local ||
		reply.Inbounds[0].Origin != "local" || len(reply.Outbounds) != 1 || reply.Outbounds[0].Origin != "shared" {
		t.Fatalf("wrong composition: %s", view.Body)
	}
	invalid, err := f.access.ReadExternalRouterDraft(ctx, p, "router-example")
	if err != nil {
		t.Fatal(err)
	}
	invalid.Editable.DisabledInbounds = nil
	if _, err := f.access.WriteExternalRouterDraft(ctx, p, invalid.Basis, invalid.Editable); err != nil {
		t.Fatal(err)
	}
	bad := f.call("GET", endpoint, "", "", "", 422)
	if bytes.Contains(bad.Body, []byte(externalRouterElement)) || bytes.Contains(bad.Body, []byte(local)) {
		t.Fatalf("partial inventory: %s", bad.Body)
	}
	f.call("GET", "/api/v1/routers/io?routerId=unknown-example", "", "", "", 404)
	if _, _, err := f.access.Owner(ctx, f.owner, &externalaccess.Change{Version: policy.Version}); err != nil {
		t.Fatal(err)
	}
	f.call("GET", endpoint, "", "", "", 404)
}
