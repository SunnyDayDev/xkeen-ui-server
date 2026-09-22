package configuration_test

import (
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"reflect"
	"testing"
)

func TestOverrideDuplicate(t *testing.T) {
	refs := []configuration.OverrideRef{{Source: inheritedSource(), Position: "/overrides/0"}, {Source: inheritedSource(), Position: "/overrides/1"}}
	before := append([]configuration.OverrideRef(nil), refs...)
	issues := configuration.ValidateOverrideSources(elementLocation.Owner, inheritedRouterLocation.Owner, refs)
	if len(issues) != 1 {
		t.Fatalf("want one duplicate error, got %+v", issues)
	}
	first := inheritedRouterLocation
	second := first
	second.Position = "/overrides/1"
	want := configuration.ElementIssue{Code: "duplicate_override", Location: second, OtherLocation: &first, ElementID: elementUUID, Source: "record"}
	if !reflect.DeepEqual(issues[0], want) {
		t.Fatalf("got %+v; want %+v", issues[0], want)
	}
	if !reflect.DeepEqual(refs, before) {
		t.Fatal("mutated references")
	}
	refs[0].Position = "/changed"
	if issues[0].OtherLocation.Position != "/overrides/0" {
		t.Fatal("diagnostic aliases input")
	}
}

func TestOverrideDistinct(t *testing.T) {
	other := inheritedSource()
	other.ElementID = "550e8400-e29b-41d4-a716-446655440001"
	for _, refs := range [][]configuration.OverrideRef{nil, {}, {{Source: inheritedSource(), Position: ""}, {Source: other, Position: "/overrides/1"}}} {
		for _, id := range []string{"router-a", "router-b"} {
			issues := configuration.ValidateOverrideSources(elementLocation.Owner, configuration.Owner{Kind: "router", ID: id}, refs)
			if len(issues) != 0 {
				t.Fatalf("valid references rejected: %+v", issues)
			}
		}
	}
}

func TestOverrideInvalid(t *testing.T) {
	for _, tc := range []struct {
		name                string
		change              func(*configuration.Owner, *configuration.Owner, *configuration.OverrideRef)
		code                string
		emptyLocation, noID bool
	}{
		{"template_kind", func(a, b *configuration.Owner, r *configuration.OverrideRef) { a.Kind = "router" }, "invalid_element_record", true, true},
		{"router_kind", func(a, b *configuration.Owner, r *configuration.OverrideRef) { b.Kind = "template" }, "invalid_element_record", true, true},
		{"empty_owner", func(a, b *configuration.Owner, r *configuration.OverrideRef) { a.ID = "" }, "invalid_element_record", true, true},
		{"utf8_owner", func(a, b *configuration.Owner, r *configuration.OverrideRef) { b.ID = string([]byte{255}) }, "invalid_element_record", true, true},
		{"position", func(a, b *configuration.Owner, r *configuration.OverrideRef) { r.Position = "bad" }, "invalid_element_record", true, true},
		{"position_escape", func(a, b *configuration.Owner, r *configuration.OverrideRef) { r.Position = "/bad~" }, "invalid_element_record", true, true},
		{"position_utf8", func(a, b *configuration.Owner, r *configuration.OverrideRef) { r.Position = "/" + string([]byte{255}) }, "invalid_element_record", true, true},
		{"empty_source", func(a, b *configuration.Owner, r *configuration.OverrideRef) { r.Source.TemplateID = "" }, "invalid_element_record", false, false},
		{"utf8_source", func(a, b *configuration.Owner, r *configuration.OverrideRef) {
			r.Source.TemplateID = string([]byte{255})
		}, "invalid_element_record", false, false},
		{"other_template", func(a, b *configuration.Owner, r *configuration.OverrideRef) { r.Source.TemplateID = "template-b" }, "source_mismatch", false, false},
		{"uuid", func(a, b *configuration.Owner, r *configuration.OverrideRef) { r.Source.ElementID = "bad-uuid" }, "invalid_element_id", false, true},
		{"uuid_before_mismatch", func(a, b *configuration.Owner, r *configuration.OverrideRef) {
			r.Source.ElementID = "bad-uuid"
			r.Source.TemplateID = "template-b"
		}, "invalid_element_id", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := elementLocation.Owner, inheritedRouterLocation.Owner
			ref := configuration.OverrideRef{Source: inheritedSource(), Position: "/overrides/0"}
			tc.change(&a, &b, &ref)
			refs := []configuration.OverrideRef{ref, ref}
			before := append([]configuration.OverrideRef(nil), refs...)
			issues := configuration.ValidateOverrideSources(a, b, refs)
			if len(issues) != 1 {
				t.Fatalf("want one issue, got %+v", issues)
			}
			loc := configuration.ElementLocation{Owner: b, Position: ref.Position}
			if tc.emptyLocation {
				loc = configuration.ElementLocation{}
			}
			id := elementUUID
			if tc.noID {
				id = ""
			}
			want := configuration.ElementIssue{Code: tc.code, Source: "record", ElementID: id, Location: loc}
			if !reflect.DeepEqual(issues[0], want) {
				t.Fatalf("got %+v; want %+v", issues[0], want)
			}
			if !reflect.DeepEqual(refs, before) {
				t.Fatal("mutated references")
			}
		})
	}
	for _, owner := range []configuration.Owner{{}, {Kind: "template", ID: ""}, {Kind: "router", ID: "wrong"}} {
		issues := configuration.ValidateOverrideSources(owner, inheritedRouterLocation.Owner, nil)
		if len(issues) != 1 || issues[0].Code != "invalid_element_record" {
			t.Fatalf("empty list hid bad owner: %+v", issues)
		}
	}
}
