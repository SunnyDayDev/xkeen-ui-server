package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestProcessExternalRouterDependencies(t *testing.T) {
	f := newExternalRuleFixture(t)
	call := f.call
	const path = "/api/v1/routers/dependencies?routerId=router-example"
	first := call("GET", path, "", "", "", 200)
	var view struct {
		Ready      bool
		References []struct{ RuleID, Kind, Status, Path string }
	}
	if json.Unmarshal(first.Body, &view) != nil || !view.Ready {
		t.Fatal(string(first.Body))
	}
	base := call("GET", externalRulesURL, "", "", "", 200)
	const secret = "synthetic-private-tag"
	raw, _ := json.Marshal(map[string]string{"afterId": "", "source": `{"inboundTag":["synthetic-private-tag"],"outboundTag":"synthetic-private-tag"}`})
	added := call("POST", externalRulesURL, base.Tag, string(raw), "", 201)
	next := call("GET", path, "", "", "", 200)
	if bytes.Contains(next.Body, []byte(secret)) || json.Unmarshal(next.Body, &view) != nil || view.Ready || len(view.References) != 2 {
		t.Fatal("wrong diagnostics", string(next.Body))
	}
	for _, ref := range view.References {
		if ref.RuleID == "" || ref.Path == "" || ref.Status == "resolved" {
			t.Fatal(ref)
		}
	}
	unchanged := call("GET", externalRulesURL, "", "", "", 200)
	if unchanged.Tag != added.Tag {
		t.Fatal("analysis changed draft")
	}
	corrupt := call("POST", externalRulesURL, added.Tag, `{"afterId":"","source":"null"}`, "", 201)
	bad := call("GET", path, "", "", "", 422)
	if bytes.Contains(bad.Body, []byte("references")) || bytes.Contains(bad.Body, []byte(secret)) {
		t.Fatal("partial data disclosed")
	}
	if current := call("GET", externalRulesURL, "", "", "", 200); current.Tag != corrupt.Tag {
		t.Fatal("failure changed draft")
	}
}
