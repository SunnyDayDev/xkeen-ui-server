package configuration_test

import (
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"reflect"
	"testing"
)

func TestTemplateIdentitySet(t *testing.T) {
	owner := elementLocation.Owner
	for _, set := range [][]configuration.ElementRef{nil, {{ID: elementUUID, Position: "/inbounds/0"}, {ID: otherUUID, Position: "/routing/rules/0"}}} {
		if issues := configuration.ValidateTemplateIDs(owner, set); len(issues) != 0 {
			t.Fatalf("valid set: %+v", issues)
		}
	}
	// A disabled rule is still supplied; no active-only filtering is allowed.
	in := []configuration.ElementRef{{ID: elementUUID, Position: "/inbounds/0"}, {ID: elementUUID, Position: "/routing/rules/0"}}
	before := append([]configuration.ElementRef(nil), in...)
	issues := configuration.ValidateTemplateIDs(owner, in)
	if len(issues) == 0 || issues[0].Code != "duplicate_element_id" {
		t.Fatalf("duplicate: %+v", issues)
	}
	issue := issues[0]
	if issue.ElementID != elementUUID || issue.Location.Owner != owner || issue.Location.Position != in[1].Position || issue.OtherLocation == nil || issue.OtherLocation.Owner != owner || issue.OtherLocation.Position != in[0].Position {
		t.Fatalf("locations: %+v", issue)
	}
	if !reflect.DeepEqual(in, before) {
		t.Fatal("changed set")
	}
}

func TestRouterIdentityCollisions(t *testing.T) {
	router := configuration.Owner{Kind: "router", ID: "router-a"}
	for _, tc := range []struct {
		name          string
		shared, local []configuration.ElementRef
		firstOwner    configuration.Owner
	}{
		{"local", nil, []configuration.ElementRef{{ID: elementUUID, Position: "/local/0"}, {ID: elementUUID, Position: "/local/1"}}, router},
		{"excluded_shared", []configuration.ElementRef{{ID: elementUUID, Position: "/routing/rules/2"}}, []configuration.ElementRef{{ID: elementUUID, Position: "/local/0"}}, elementLocation.Owner},
		{"new_shared", []configuration.ElementRef{{ID: otherUUID, Position: "/outbounds/0"}, {ID: elementUUID, Position: "/routing/rules/3"}}, []configuration.ElementRef{{ID: elementUUID, Position: "/local/0"}}, elementLocation.Owner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issues := configuration.ValidateRouterLocalIDs(elementLocation.Owner, tc.shared, router, tc.local)
			if len(issues) == 0 || issues[0].Code != "duplicate_element_id" {
				t.Fatalf("collision: %+v", issues)
			}
			issue := issues[0]
			if issue.Location.Owner != router || issue.OtherLocation == nil || issue.OtherLocation.Owner != tc.firstOwner || issue.ElementID != elementUUID {
				t.Fatalf("owners: %+v", issue)
			}
		})
	}
}

func TestIdentitySetBoundaries(t *testing.T) {
	set := []configuration.ElementRef{{ID: elementUUID, Position: "/outbounds/0"}}
	for _, ownerID := range []string{"template-a", "template-b"} {
		if issues := configuration.ValidateTemplateIDs(configuration.Owner{Kind: "template", ID: ownerID}, set); len(issues) != 0 {
			t.Fatal(issues)
		}
	}
	for _, ownerID := range []string{"router-a", "router-b"} {
		if issues := configuration.ValidateRouterLocalIDs(elementLocation.Owner, nil, configuration.Owner{Kind: "router", ID: ownerID}, set); len(issues) != 0 {
			t.Fatal(issues)
		}
	}
	// The candidate replaces the old record, keeping its ID; only one ref exists.
	candidate := append([]configuration.ElementRef(nil), set...)
	if issues := configuration.ValidateTemplateIDs(elementLocation.Owner, candidate); len(issues) != 0 {
		t.Fatal(issues)
	}
	for _, tc := range []struct {
		name  string
		owner configuration.Owner
		set   []configuration.ElementRef
		code  string
	}{
		{"invalid_id", elementLocation.Owner, []configuration.ElementRef{{ID: "demo-private-value", Position: "/outbounds/0"}}, "invalid_element_id"},
		{"empty_owner", configuration.Owner{Kind: "template"}, set, "invalid_element_record"},
		{"wrong_kind", configuration.Owner{Kind: "router", ID: "router-a"}, set, "invalid_element_record"},
		{"invalid_position", elementLocation.Owner, []configuration.ElementRef{{ID: elementUUID, Position: "not-a-pointer"}}, "invalid_element_record"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issues := configuration.ValidateTemplateIDs(tc.owner, tc.set)
			if len(issues) == 0 || issues[0].Code != tc.code {
				t.Fatalf("issues %+v; want %s", issues, tc.code)
			}
			if tc.code == "invalid_element_id" && issues[0].ElementID != "" {
				t.Fatal("exposed invalid ID")
			}
		})
	}
	for _, owner := range []configuration.Owner{{Kind: "template", ID: "not-router"}, {Kind: "router"}} {
		if issues := configuration.ValidateRouterLocalIDs(elementLocation.Owner, nil, owner, nil); len(issues) == 0 {
			t.Fatal("accepted invalid empty local set context")
		}
	}
}
