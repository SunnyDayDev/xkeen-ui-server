package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestProcessExternalRuleState(t *testing.T) {
	f := newExternalRuleFixture(t)
	call := f.call
	prefix := "/api/v1/routers/rules/state?routerId=router-example&elementId="
	read := func(id string) externalRouterResponse { return call("GET", prefix+id, "", "", "", 200) }
	check := func(r externalRouterResponse, personal, effective, excluded bool) {
		t.Helper()
		var x struct{ PersonalDisabled, Disabled, Excluded bool }
		if json.Unmarshal(r.Body, &x) != nil || x.PersonalDisabled != personal || x.Disabled != effective || x.Excluded != excluded || r.Tag == "" {
			t.Fatal("incorrect rule state", string(r.Body))
		}
	}
	initial := read(externalDisabledRule)
	check(initial, false, true, false)
	current := call("PATCH", prefix+externalDisabledRule, initial.Tag, `{"disabled":true}`, "", 200)
	check(current, true, true, false)
	restored := call("PATCH", prefix+externalDisabledRule, current.Tag, `{"disabled":false}`, "", 200)
	check(restored, false, true, false)
	call("PATCH", prefix+externalDisabledRule, current.Tag, `{"disabled":true}`, "", 412)
	x := read(externalExcludedRule)
	check(x, false, false, true)
	call("PATCH", prefix+externalExcludedRule, x.Tag, `{"disabled":null}`, "", 400)
	fresh := read(externalExcludedRule)
	if !bytes.Equal(x.Body, fresh.Body) || fresh.Tag != x.Tag {
		t.Fatal("invalid input changed state")
	}

	// A literal JSON rule can be saved before it is suitable for active routing.
	// It can be disabled to repair the draft, but cannot be re-enabled as active.
	collection := call("GET", externalRulesURL, "", "", "", 200)
	bad := call("POST", externalRulesURL, collection.Tag, `{"afterId":"","source":"null"}`, "", 201)
	var rows struct{ Rules []struct{ ID string } }
	if json.Unmarshal(bad.Body, &rows) != nil || len(rows.Rules) != 3 {
		t.Fatal("missing local rule")
	}
	local := rows.Rules[0].ID
	before := read(local)
	off := call("PATCH", prefix+local, before.Tag, `{"disabled":true}`, "", 200)
	check(off, true, true, false)
	call("PATCH", prefix+local, off.Tag, `{"disabled":false}`, "", 409)
	if saved := read(local); saved.Tag != off.Tag || !bytes.Equal(saved.Body, off.Body) {
		t.Fatal("invalid enable was written")
	}
}
