package agentrevision

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func snapshotFixture() Snapshot {
	return Snapshot{Number: 8, Profile: configuration.XrayIdentityVersion, Files: []configuration.CatalogFile{
		{Name: "01_log.json", Content: []byte(" {}\n")}, {Name: "02_api.json", Content: []byte("{}")},
		{Name: "03_inbounds.json", Content: []byte("{}")}, {Name: "04_outbounds.json", Content: []byte("{}")},
		{Name: "05_routing.json", Content: []byte("{}")}, {Name: "06_extra.json", Content: []byte("{}")},
	}}
}

func TestDesiredCodecUsesStableScopedRepresentation(t *testing.T) {
	snapshot := snapshotFixture()
	tag, body, err := Encode("one.example", snapshot)
	if err != nil || len(tag) != 69 {
		t.Fatalf("encode = %q, %v", tag, err)
	}
	slices.Reverse(snapshot.Files)
	sameTag, sameBody, err := Encode("one.example", snapshot)
	if err != nil || tag != sameTag || !bytes.Equal(body, sameBody) {
		t.Fatal("input order changes the strong representation")
	}
	otherTag, otherBody, err := Encode("two.example", snapshot)
	if err != nil || otherTag == tag || !bytes.Equal(body, otherBody) {
		t.Fatal("strong tag does not distinguish devices")
	}
	decoded, err := Decode("one.example", tag, body)
	if err != nil || decoded.Number != 8 || !bytes.Equal(decoded.Files[0].Content, []byte(" {}\n")) {
		t.Fatalf("decode = %v, %v", decoded, err)
	}
}

func TestDesiredCodecRejectsIncompleteAlteredAndOversizeSnapshots(t *testing.T) {
	tag, body, err := Encode("one.example", snapshotFixture())
	if err != nil {
		t.Fatal(err)
	}
	for name, damaged := range map[string][]byte{
		"truncated":      body[:len(body)-1],
		"altered bytes":  bytes.Replace(body, []byte("IHt9Cg=="), []byte("e30="), 1),
		"duplicate name": bytes.Replace(body, []byte("06_extra.json"), []byte("05_routing.json"), 1),
		"wrong filename": bytes.Replace(body, []byte("06_extra.json"), []byte("../extra.json"), 1),
		"leading zero":   bytes.Replace(body, []byte(`"8"`), []byte(`"08"`), 1),
		"oversize":       make([]byte, MaxBody+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode("one.example", tag, damaged); !errors.Is(err, ErrInvalid) {
				t.Fatalf("malformed snapshot = %v", err)
			}
		})
	}
	var object struct {
		Revision string            `json:"revision"`
		Profile  string            `json:"profile"`
		Files    []json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatal(err)
	}
	object.Files = object.Files[:5]
	missingOptional, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []struct {
		scope   string
		payload []byte
	}{{"one.example", missingOptional}, {"two.example", body}} {
		if _, err := Decode(attempt.scope, tag, attempt.payload); !errors.Is(err, ErrInvalid) {
			t.Fatalf("mismatched complete list or scope = %v", err)
		}
	}
}
