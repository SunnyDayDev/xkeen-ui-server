package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

const externalLocalInbound = "550e8400-e29b-41d4-a716-446655440071"
const externalLocalOutbound = "550e8400-e29b-41d4-a716-446655440072"
const externalOtherOutbound = "550e8400-e29b-41d4-a716-446655440073"

func fixtureExternalLocalIO(t *testing.T, f externalRouterFixture) {
	t.Helper()
	ctx := t.Context()
	p, err := f.access.Admit(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := f.access.ReadExternalRouterDraft(ctx, p, "router-example")
	if err != nil {
		t.Fatal(err)
	}
	snap.Editable.Groups = append(snap.Editable.Groups,
		routersettings.Group{Name: "inbounds", Local: []routersettings.LocalElement{
			{ID: externalLocalInbound, Content: json.RawMessage(`{"tag":"private-inbound"}`)},
		}},
		routersettings.Group{Name: "outbounds", Local: []routersettings.LocalElement{
			{ID: externalLocalOutbound, Content: json.RawMessage(`{"tag":"private-prefix-one"}`)},
			{ID: externalOtherOutbound, Content: json.RawMessage(`{"tag":"private-prefix-two"}`)},
		}},
		routersettings.Group{Name: "routing", Local: []routersettings.LocalElement{
			{ID: "550e8400-e29b-41d4-a716-446655440074", Content: json.RawMessage(`{"balancers":[{"tag":"balancer","selector":["private-prefix"]}]}`)},
		}},
	)
	if _, err := f.access.WriteExternalRouterDraft(ctx, p, snap.Basis, snap.Editable); err != nil {
		t.Fatal(err)
	}
}

func TestProcessExternalRemoveRouterIO(t *testing.T) {
	f := newExternalRuleFixture(t)
	fixtureExternalLocalIO(t, f)
	const endpoint = "/api/v1/routers/io/item?routerId=router-example&section="
	const in = endpoint + "inbounds&elementId=" + externalLocalInbound
	const firstOut = endpoint + "outbounds&elementId=" + externalLocalOutbound
	const lastOut = endpoint + "outbounds&elementId=" + externalOtherOutbound
	const shared = endpoint + "outbounds&elementId=" + externalRouterElement
	const confirmation = "&confirm=remove"
	call := f.call
	inbound := call("GET", in, "", "", "", 200)
	if inbound.Tag == "" || bytes.Contains(inbound.Body, []byte("private-inbound")) {
		t.Fatal(string(inbound.Body))
	}
	blocked := call("GET", externalRulesURL, "", "", "", 200)
	call("POST", externalRulesURL, blocked.Tag, `{"afterId":"","source":"{\"inboundTag\":[\"private-inbound\"],\"outboundTag\":\"private-prefix-one\"}"}`, "", 201)
	call("DELETE", in+confirmation, inbound.Tag, "", "", 412)
	inbound = call("GET", in, "", "", "", 200)
	blockedDelete := call("DELETE", in+confirmation, inbound.Tag, "", "", 409)
	if bytes.Contains(blockedDelete.Body, []byte("private-inbound")) {
		t.Fatal(string(blockedDelete.Body))
	}
	outbound := call("GET", firstOut, "", "", "", 200)
	direct := call("DELETE", firstOut+confirmation, outbound.Tag, "", "", 409)
	if bytes.Contains(direct.Body, []byte("private-prefix")) {
		t.Fatal(string(direct.Body))
	}
	rules := call("GET", externalRulesURL, "", "", "", 200)
	var body struct {
		Rules []struct {
			ID     string
			Origin string
		}
	}
	if json.Unmarshal(rules.Body, &body) != nil || len(body.Rules) != 3 || body.Rules[0].Origin != "local" {
		t.Fatal(string(rules.Body))
	}
	state := "/api/v1/routers/rules/state?routerId=router-example&elementId=" + body.Rules[0].ID
	status := call("GET", state, "", "", "", 200)
	call("PATCH", state, status.Tag, `{"disabled":true}`, "", 200)
	rules = call("GET", externalRulesURL, "", "", "", 200)
	call("POST", externalRulesURL, rules.Tag, `{"afterId":"","source":"{\"balancerTag\":\"balancer\"}"}`, "", 201)
	inbound = call("GET", in, "", "", "", 200)
	call("DELETE", in, inbound.Tag, "", "", 400)
	call("DELETE", in+confirmation, inbound.Tag, "", "", 200)
	call("GET", in, "", "", "", 404)
	outbound = call("GET", firstOut, "", "", "", 200)
	call("DELETE", firstOut+confirmation, outbound.Tag, "", "", 200)
	call("GET", firstOut, "", "", "", 404)
	outbound = call("GET", lastOut, "", "", "", 200)
	call("DELETE", lastOut+confirmation, outbound.Tag, "", "", 409)
	stored := call("GET", lastOut, "", "", "", 200)
	if stored.Tag != outbound.Tag {
		t.Fatal("blocked removal modified draft")
	}
	candidate := call("GET", shared, "", "", "", 200)
	result := call("DELETE", shared+confirmation, candidate.Tag, "", "", 200)
	if !bytes.Contains(result.Body, []byte(`"effect":"excluded"`)) {
		t.Fatal(string(result.Body))
	}
}
