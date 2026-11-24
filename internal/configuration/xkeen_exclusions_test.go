package configuration

import (
	"encoding/json"
	"testing"
)

func TestXKeenExclusionsAssembly(t *testing.T) {
	src := &ExclusionSource{Text: " # head\r\n192.0.2.4/32 # host\r\n192.0.2.4\n${up.net}\n2001:db8::1/128\n2001:db8::1\n", Variables: []VariableDefinition{{Name: "up.net", Type: "string", Default: json.RawMessage(`"2001:db8::/32"`)}}}
	f, issue := AssembleXKeenExclusions(src, nil, nil)
	if issue != nil || f == nil || f.Name != "ip_exclude.lst" || string(f.Content) != "192.0.2.4\n2001:db8::/32\n2001:db8::1\n" {
		t.Fatalf("default: %#v %#v", f, issue)
	}
	vals := map[string]json.RawMessage{"up.net": json.RawMessage(`"198.51.100.0/24"`)}
	f, issue = AssembleXKeenExclusions(src, vals, nil)
	if issue != nil || string(f.Content) != "192.0.2.4\n198.51.100.0/24\n2001:db8::1\n" {
		t.Fatal("personal")
	}
	empty := ""
	f, issue = AssembleXKeenExclusions(src, vals, &empty)
	if issue != nil || f == nil || len(f.Content) != 0 {
		t.Fatal("explicit clear")
	}
	f, issue = AssembleXKeenExclusions(nil, nil, nil)
	if f != nil || issue != nil {
		t.Fatal("absent")
	}
	literal := "${up.net}"
	if f, issue = AssembleXKeenExclusions(src, vals, &literal); f != nil || issue == nil || issue.Line != 1 {
		t.Fatal("replacement interpreted")
	}
}
func TestXKeenExclusionsRejectWholeFile(t *testing.T) {
	for _, bad := range []string{"0.0.0.0/0", "::/0", "192.0.2.3/24", "fe80::1%en0", "::ffff:192.0.2.1", "0x7f000001", "example.net", "192.0.2.4:1234", "\xff", "192.0.2.3 192.0.2.5"} {
		f, e := AssembleXKeenExclusions(&ExclusionSource{Text: "192.0.2.1\n" + bad}, nil, nil)
		if f != nil || e == nil || e.Line != 2 {
			t.Errorf("invalid %q: %v %v", bad, f, e)
		}
	}
	for _, v := range []json.RawMessage{nil, []byte(`"192.0.2.4\n198.51.100.2"`), []byte(`"${again}"`), []byte("42"), []byte("null")} {
		s := &ExclusionSource{Text: "\n${name}", Variables: []VariableDefinition{{Name: "name", Type: "string"}}}
		f, e := AssembleXKeenExclusions(s, map[string]json.RawMessage{"name": v}, nil)
		if f != nil || e == nil || e.Line != 2 {
			t.Fatal("unexpected substitution", v)
		}
	}
}
