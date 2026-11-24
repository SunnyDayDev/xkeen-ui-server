package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestProcessExternalLocalRuleAdd(t *testing.T) {
	fixture := newExternalRouterFixture(t)
	ctx := t.Context()
	if _, _, err := fixture.access.Owner(ctx, fixture.owner,
		&externalaccess.Change{Version: fixture.settings.Version, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	const excluded = "550e8400-e29b-41d4-a716-446655440030"
	const disabled = "550e8400-e29b-41d4-a716-446655440031"
	shared := `{"name":"rules","elements":[
		{"disabled":false,"record":{"id":"` + excluded + `","content":"{\"ruleTag\":\"excluded-rule\"}","variables":[]}},
		{"disabled":true,"record":{"id":"` + disabled + `","content":"{\"ruleTag\":\"disabled-rule\"}","variables":[]}}]}`
	if _, err := fixture.db.ExecContext(ctx, `UPDATE accepted_templates
		SET draft=CAST(json_insert(CAST(draft AS TEXT), '$[#]', json(?)) AS BLOB)`, shared); err != nil {
		t.Fatal(err)
	}
	permit, err := fixture.access.Admit(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	base, err := fixture.access.ReadExternalRouterDraft(ctx, permit, "router-example")
	if err != nil {
		t.Fatal(err)
	}
	base.Editable.Groups = append(base.Editable.Groups, routersettings.Group{
		Name:       "rules",
		Exclusions: []configuration.ElementSource{{TemplateID: base.Basis.TemplateID, ElementID: excluded}},
	})
	if _, err := fixture.access.WriteExternalRouterDraft(ctx, permit, base.Basis, base.Editable); err != nil {
		t.Fatal(err)
	}
	const collection = "/api/v1/routers/rules?routerId=router-example"
	call := fixture.call
	view := call("GET", collection, "", "", "", 200)
	if view.Tag == "" || !bytes.Contains(view.Body, []byte(`"excluded":true`)) ||
		!bytes.Contains(view.Body, []byte(`"disabled":true`)) {
		t.Fatalf("complete order lacks inactive members: %s", view.Body)
	}
	for _, request := range []string{
		`{"source":"{}"}`, `{"afterId":null,"source":"{}"}`, `{"afterId":"","source":null}`,
		`{"afterId":"","source":"{}","id":"` + disabled + `"}`,
	} {
		call("POST", collection, view.Tag, request, "", 400)
	}
	call("POST", collection, view.Tag, `{"afterId":"","source":"{"}`, "", 422)
	call("POST", collection, view.Tag, `{"afterId":"`+externalRouterElement+`","source":"{}"}`, "", 409)
	exact := " {\r\n\"ruleTag\":\"local-test\",\"number\":9007199254740993\n}\r\n"
	raw, err := json.Marshal(map[string]string{"afterId": disabled, "source": exact})
	if err != nil {
		t.Fatal(err)
	}
	result := call("POST", collection, view.Tag, string(raw), "", 201)
	var orders struct{ Rules []struct{ ID, Origin string } }
	if json.Unmarshal(result.Body, &orders) != nil || len(orders.Rules) != 3 ||
		orders.Rules[0].ID != excluded || orders.Rules[1].ID != disabled ||
		orders.Rules[2].Origin != "local" || !configuration.ValidElementID(orders.Rules[2].ID) || view.Tag == result.Tag {
		t.Fatalf("local insertion misplaced: %s", result.Body)
	}
	call("POST", collection, view.Tag, string(raw), "", 412)
	if read := call("GET", collection, "", "", "", 200); read.Tag != result.Tag || !bytes.Equal(read.Body, result.Body) {
		t.Fatal("POST did not identify committed order")
	}
	stored, err := fixture.access.ReadExternalRouterDraft(ctx, permit, "router-example")
	if err != nil || stored.OrderBasis == nil || len(stored.Editable.Groups) != 1 ||
		len(stored.Editable.Groups[0].Local) != 1 ||
		string(stored.Editable.Groups[0].Local[0].Content) != exact ||
		len(stored.Editable.Groups[0].Exclusions) != 1 || stored.Editable.Groups[0].Exclusions[0].ElementID != excluded {
		t.Fatalf("exact local bytes, exclusion or basis missing: %v", err)
	}
}
