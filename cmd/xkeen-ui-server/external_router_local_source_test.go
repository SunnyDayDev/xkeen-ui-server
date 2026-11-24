package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
)

func TestProcessExternalLocalRuleSource(t *testing.T) {
	f := newExternalRuleFixture(t)
	call := f.call
	base := call("GET", externalRulesURL, "", "", "", 200)
	added := call("POST", externalRulesURL, base.Tag, `{"afterId":"","source":"{}"}`, "", 201)
	var rules struct{ Rules []struct{ ID string } }
	if err := json.Unmarshal(added.Body, &rules); err != nil || len(rules.Rules) != 3 {
		t.Fatal(err)
	}
	prefix := "/api/v1/routers/rules/local?routerId=router-example&elementId="
	path := prefix + rules.Rules[0].ID
	permit, err := f.access.Admit(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.access.ReadExternalRouterDraft(t.Context(), permit, "router-example")
	if err != nil {
		t.Fatal(err)
	}
	current := call("GET", path, "", "", "", 200)
	call("GET", prefix+externalExcludedRule, "", "", "", 404)
	call("PATCH", path, "", `{"source":"null"}`, "", 428)
	call("PATCH", path, current.Tag, `{"source":"{"}`, "", 422)
	call("PATCH", path, added.Tag, `{"source":"{}"}`, "", 412)
	literal := " { \"example\": \"${literal}\", \"large\":9007199254740993, \"value\":null }\n"
	body, _ := json.Marshal(map[string]string{"source": literal})
	changed := call("PATCH", path, current.Tag, string(body), "", 200)
	fresh := call("GET", path, "", "", "", 200)
	var result struct{ Source string }
	if json.Unmarshal(fresh.Body, &result) != nil || result.Source != literal || changed.Tag == current.Tag || fresh.Tag != changed.Tag || !bytes.Equal(changed.Body, fresh.Body) {
		t.Fatal("wrong literal or representation")
	}
	call("PATCH", path, current.Tag, `{"source":"null"}`, "", 412)

	after, err := f.access.ReadExternalRouterDraft(t.Context(), permit, "router-example")
	if err != nil || !reflect.DeepEqual(before.OrderBasis, after.OrderBasis) || !reflect.DeepEqual(before.Editable.PersonalOrder, after.Editable.PersonalOrder) || !reflect.DeepEqual(before.Editable.DisabledRules, after.Editable.DisabledRules) || !reflect.DeepEqual(before.Editable.Groups[0].Exclusions, after.Editable.Groups[0].Exclusions) {
		t.Fatal("source edit changed order or rule state", err)
	}
	null := call("PATCH", path, changed.Tag, `{"source":"null"}`, "", 200)
	if !bytes.Contains(null.Body, []byte(`"source":"null"`)) {
		t.Fatal("root null rejected")
	}
	settings, _, err := f.access.Owner(t.Context(), f.owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.access.Owner(t.Context(), f.owner, &externalaccess.Change{Version: settings.Version, Enabled: false, Protected: false}); err != nil {
		t.Fatal(err)
	}
	call("GET", path, "", "", "", 404)
	if _, err := f.access.WriteExternalRouterDraft(t.Context(), permit, after.Basis, after.Editable); !errors.Is(err, externalaccess.ErrDisabled) && !errors.Is(err, externalaccess.ErrUnauthorized) {
		t.Fatal("old permit reached protected writer", err)
	}
}
