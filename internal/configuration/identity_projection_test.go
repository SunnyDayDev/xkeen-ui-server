package configuration_test

import (
	"bytes"
	"encoding/json"
	c "github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"testing"
)

func projectionInput(position string, content string) c.IdentityProjection {
	return c.IdentityProjection{ElementID: selectionID, Location: c.ElementLocation{Owner: c.Owner{Kind: "router", ID: "router-a"}, Position: "/local/0"}, Position: position, Version: c.XrayIdentityVersion, Content: json.RawMessage(content)}
}

func TestProjectionNativeTag(t *testing.T) {
	in := projectionInput("outbound", `{"tag":"direct","settings":{"flag":true}}`)
	before := bytes.Clone(in.Content)
	got, issues := c.ProjectElementIdentity(in)
	if len(issues) != 0 || !bytes.Contains(got, []byte(`"_xkeenUiId":"`+selectionID+`"`)) || !bytes.Contains(got, []byte(`"tag":"direct"`)) {
		t.Fatalf("missing projection: %s %+v", got, issues)
	}
	got[0] = '!'
	if !bytes.Equal(in.Content, before) {
		t.Fatal("mutated input")
	}
}

func TestProjectionPositionsAndConflict(t *testing.T) {
	for _, position := range []string{"inbound", "outbound", "rule", "fakedns", "log", "dns", "routing", "policy", "stats", "observatory"} {
		t.Run(position, func(t *testing.T) {
			in := projectionInput(position, `{"nested":{},"values":[{}]}`)
			got, issues := c.ProjectElementIdentity(in)
			if len(issues) != 0 || bytes.Count(got, []byte(`_xkeenUiId`)) != 1 {
				t.Fatalf("%s %+v", got, issues)
			}
			in.Content = got
			again, issues := c.ProjectElementIdentity(in)
			if len(issues) != 0 || !bytes.Equal(got, again) {
				t.Fatal("not idempotent")
			}
		})
	}
	for _, raw := range []string{`{"_xkeenUiId":"different"}`, `{"_xkeenUiId":42}`} {
		in := projectionInput("outbound", raw)
		got, issues := c.ProjectElementIdentity(in)
		if got != nil || len(issues) != 1 || issues[0].Code != "identity_marker_conflict" || issues[0].Path == nil || *issues[0].Path != "/_xkeenUiId" || string(in.Content) != raw {
			t.Fatalf("conflict overwritten %s %+v", got, issues)
		}
	}
}

func TestProjectionReadyObject(t *testing.T) {
	raw, issues := c.AssembleElement(selectionID, json.RawMessage(`"${object}"`), []c.VariableDefinition{{Name: "object", Type: "object", Default: json.RawMessage(`{"tag":"direct"}`)}}, nil)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	in := projectionInput("inbound", string(raw))
	got, fail := c.ProjectElementIdentity(in)
	if len(fail) != 0 || !bytes.Contains(got, []byte(selectionID)) {
		t.Fatalf("%s %+v", got, fail)
	}
}

func TestProjectionUnsupportedAndPrecision(t *testing.T) {
	for _, position := range []string{"api", "unknown", "hosts", "servers"} {
		in := projectionInput(position, `{"_xkeenUiId":false,"n":9007199254740993,"list":[false,null]}`)
		got, fail := c.ProjectElementIdentity(in)
		if len(fail) != 0 || !bytes.Equal(got, in.Content) {
			t.Fatalf("unsupported changed: %s %+v", got, fail)
		}
	}
	for _, raw := range []string{`[]`, `[{},9007199254740993]`, `null`, `false`, `42`, `"literal"`} {
		in := projectionInput("outbound", raw)
		got, fail := c.ProjectElementIdentity(in)
		if len(fail) != 0 || !bytes.Equal(got, in.Content) {
			t.Fatalf("wrapped value: %s %+v", got, fail)
		}
	}
	in := projectionInput("outbound", `{"n":9007199254740993}`)
	in.Version = "other"
	got, fail := c.ProjectElementIdentity(in)
	if len(fail) != 0 || !bytes.Equal(got, in.Content) {
		t.Fatalf("wrong version marked: %s %+v", got, fail)
	}
}

func TestProjectionInvalidMetadataAndJSON(t *testing.T) {
	for _, kind := range []string{"id", "location", "json", "duplicate"} {
		in := projectionInput("outbound", `{}`)
		switch kind {
		case "id":
			in.ElementID = "private-invalid-id"
		case "location":
			in.Location.Owner.Kind = ""
		case "json":
			in.Content = json.RawMessage(`{`)
		case "duplicate":
			in.Content = json.RawMessage(`{"x":1,"x":2}`)
		}
		got, fail := c.ProjectElementIdentity(in)
		if got != nil || len(fail) == 0 || (kind == "id" && fail[0].ElementID != "") {
			t.Fatalf("unsafe metadata: %s %+v", got, fail)
		}
	}
}
