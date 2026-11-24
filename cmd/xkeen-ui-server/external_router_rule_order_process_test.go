package main

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
)

func TestProcessExternalRouterRuleOrder(t *testing.T) {
	fixture := newExternalRuleFixture(t)
	call := fixture.call
	state := func(response externalRouterResponse, wantPersonal bool, wantIDs ...string) {
		t.Helper()
		var result struct {
			PersonalOrder bool
			Rules         []struct{ ID string }
		}
		if json.Unmarshal(response.Body, &result) != nil || result.PersonalOrder != wantPersonal || response.Tag == "" {
			t.Fatalf("invalid order response: %s", response.Body)
		}
		ids := make([]string, len(result.Rules))
		for i, entry := range result.Rules {
			ids[i] = entry.ID
		}
		if !slices.Equal(ids, wantIDs) {
			t.Fatalf("reorder: got %v want %v", ids, wantIDs)
		}
	}
	const a, b = externalExcludedRule, externalDisabledRule
	base := call("GET", externalRulesURL, "", "", "", 200)
	state(base, false, a, b)
	for _, invalid := range []string{
		`{"action":"move","ruleId":"` + b + `"}`,
		`{"action":"move","ruleId":"` + b + `","position":null}`,
		`{"action":"move","ruleId":"` + b + `","position":2}`,
		`{"action":"move","ruleId":"` + b + `","position":0,"confirm":true}`,
		`{"action":"reset"}`, `{"action":"reset","confirm":false}`,
		`{"action":"reset","confirm":true,"ruleId":null}`, `{"action":"reset","confirm":true,"position":null}`,
	} {
		call("PATCH", externalRulesURL, base.Tag, invalid, "", 400)
	}
	const moveFirst = `{"action":"move","ruleId":"` + b + `","position":0}`
	const reset = `{"action":"reset","confirm":true}`
	moved := call("PATCH", externalRulesURL, base.Tag, moveFirst, "", 200)
	state(moved, true, b, a)
	call("PATCH", externalRulesURL, base.Tag, reset, "", 412)
	inherited := call("PATCH", externalRulesURL, moved.Tag, reset, "", 200)
	state(inherited, false, a, b)
	permit, err := fixture.access.Admit(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.access.ReadExternalRouterDraft(t.Context(), permit, "router-example")
	if err != nil || stored.OrderBasis != nil || stored.Editable.PersonalOrder != nil {
		t.Fatalf("reset kept explicit order or proof: %v", err)
	}

	added := call("POST", externalRulesURL, inherited.Tag, `{"afterId":"`+a+`","source":"{\"ruleTag\":\"local\"}"}`, "", 201)
	var inserted struct{ Rules []struct{ ID string } }
	if json.Unmarshal(added.Body, &inserted) != nil || len(inserted.Rules) != 3 {
		t.Fatalf("no new local rule: %s", added.Body)
	}
	local := inserted.Rules[1].ID
	state(added, true, a, local, b)
	reversed := call("PATCH", externalRulesURL, added.Tag, moveFirst, "", 200)
	state(reversed, true, b, a, local)
	mixed := call("PATCH", externalRulesURL, reversed.Tag, `{"action":"move","ruleId":"`+local+`","position":1}`, "", 200)
	state(mixed, true, b, local, a)
	restored := call("PATCH", externalRulesURL, mixed.Tag, reset, "", 200)
	state(restored, true, a, local, b)
	if reread := call("GET", externalRulesURL, "", "", "", 200); reread.Tag != restored.Tag || !bytes.Equal(reread.Body, restored.Body) {
		t.Fatal("patch receipt does not identify committed order")
	}
}
