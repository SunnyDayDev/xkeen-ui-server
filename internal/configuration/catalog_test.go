package configuration_test

import (
	"bytes"
	"encoding/json"
	c "github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"reflect"
	"testing"
)

func emptyCatalog() c.CatalogInput {
	group := func() *c.RouterSelectionInput { v := selectionInput(); return &v }
	return c.CatalogInput{Template: selectionInput().Template, Router: selectionInput().Router, Log: group(), API: group(), Inbounds: group(), Outbounds: group(), Routing: group(), Rules: group()}
}
func catalog(t *testing.T, in c.CatalogInput) *c.XrayCatalog {
	t.Helper()
	got, issues := c.AssembleXrayCatalog(in)
	if got == nil || len(issues) != 0 {
		t.Fatalf("assembly failed: %+v", issues)
	}
	return got
}
func catalogFailure(t *testing.T, in c.CatalogInput, code string) c.CatalogIssue {
	t.Helper()
	got, issues := c.AssembleXrayCatalog(in)
	if got != nil || len(issues) == 0 || issues[0].Code != code {
		t.Fatalf("want %s, got %+v %+v", code, got, issues)
	}
	return issues[0]
}
func fileValue(t *testing.T, got *c.XrayCatalog, name string) map[string]any {
	t.Helper()
	for _, f := range got.Files {
		if f.Name == name {
			var out map[string]any
			d := json.NewDecoder(bytes.NewReader(f.Content))
			d.UseNumber()
			if err := d.Decode(&out); err != nil {
				t.Fatal(err)
			}
			return out
		}
	}
	t.Fatalf("missing %s", name)
	return nil
}
func TestCatalogExplicitEmptyAndMissing(t *testing.T) {
	got := catalog(t, emptyCatalog())
	want := []struct{ name, raw string }{{"01_log.json", `{"log":{}}`}, {"02_api.json", `{}`}, {"03_inbounds.json", `{"inbounds":[]}`}, {"04_outbounds.json", `{"outbounds":[]}`}, {"05_routing.json", `{"routing":{"rules":[]}}`}}
	if len(got.Files) != 5 {
		t.Fatalf("files: %+v", got.Files)
	}
	for i, item := range want {
		var a, b any
		json.Unmarshal(got.Files[i].Content, &a)
		json.Unmarshal([]byte(item.raw), &b)
		if got.Files[i].Name != item.name || !reflect.DeepEqual(a, b) {
			t.Fatalf("bad empty file: %+v", got.Files[i])
		}
	}
	for _, name := range []string{"log", "api", "inbounds", "outbounds", "routing", "rules"} {
		t.Run(name, func(t *testing.T) {
			in := emptyCatalog()
			switch name {
			case "log":
				in.Log = nil
			case "api":
				in.API = nil
			case "inbounds":
				in.Inbounds = nil
			case "outbounds":
				in.Outbounds = nil
			case "routing":
				in.Routing = nil
			case "rules":
				in.Rules = nil
			}
			issue := catalogFailure(t, in, "missing_catalog_section")
			if issue.Location.Position != "/"+name {
				t.Fatal(issue)
			}
		})
	}
}

func localCatalog(group *c.RouterSelectionInput, id, position, raw string) {
	group.Local = append(group.Local, c.LocalContent{ID: id, Position: position, Content: json.RawMessage(raw)})
}
func sharedCatalog(group *c.RouterSelectionInput, id, position, raw string) {
	group.Shared = append(group.Shared, c.SharedElement{Element: c.Element{ID: id, Content: raw}, Position: position})
}

func TestCatalogMetadataAndSingleton(t *testing.T) {
	tests := []struct {
		name, code string
		edit       func(*c.CatalogInput)
	}{
		{"owner", "invalid_element_record", func(in *c.CatalogInput) { in.API.Router.ID = "other" }},
		{"id", "invalid_element_id", func(in *c.CatalogInput) { localCatalog(in.API, "invalid-sensitive-id", "/api/0", `{`) }},
		{"cross section", "duplicate_element_id", func(in *c.CatalogInput) {
			localCatalog(in.Inbounds, selectionID, "/inbounds/0", `{`)
			localCatalog(in.Rules, selectionID, "/rules/0", `{`)
			in.DisabledInbounds = []c.ElementRef{{ID: selectionID, Position: "/disabled/0"}}
		}},
		{"other IDs", "duplicate_element_id", func(in *c.CatalogInput) {
			localCatalog(in.API, selectionID, "/api/0", `{`)
			in.OtherLocal = []c.ElementRef{{ID: selectionID, Position: "/other/0"}}
		}},
		{"singleton", "duplicate_catalog_section", func(in *c.CatalogInput) {
			sharedCatalog(in.Log, selectionID, "/log/0", `{}`)
			localCatalog(in.Log, selectionLocalID, "/local/log", `{}`)
		}},
		{"exclusion", "duplicate_exclusion", func(in *c.CatalogInput) {
			sharedCatalog(in.Log, selectionID, "/log/0", `{`)
			source := c.ElementSource{TemplateID: in.Template.ID, ElementID: selectionID}
			in.Log.Exclusions = []c.SharedExclusion{{Source: source, Position: "/exclusions/0"}, {Source: source, Position: "/exclusions/1"}}
		}},
		{"source", "source_mismatch", func(in *c.CatalogInput) {
			sharedCatalog(in.Log, selectionID, "/log/0", `{`)
			in.Log.Overrides = []c.LocatedContentOverride{{Position: "/override/0", Override: c.ContentOverride{Source: c.ElementSource{TemplateID: "other", ElementID: selectionID}, Mode: c.ContentModeVariables}}}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := emptyCatalog()
			tc.edit(&in)
			issue := catalogFailure(t, in, tc.code)
			if tc.code == "duplicate_element_id" || tc.code == "duplicate_catalog_section" || tc.code == "duplicate_exclusion" {
				if issue.OtherLocation == nil {
					t.Fatal("missing conflict location")
				}
			}
			if tc.code == "invalid_element_id" && issue.ElementID != "" {
				t.Fatal("unsafe id")
			}
		})
	}
}

func TestCatalogActiveSingletonAndAPI(t *testing.T) {
	in := emptyCatalog()
	localCatalog(in.Log, selectionID, "/log/0", `{"loglevel":"warning"}`)
	localCatalog(in.API, selectionLocalID, "/api/0", `{}`)
	got := catalog(t, in)
	if fileValue(t, got, "01_log.json")["log"].(map[string]any)["loglevel"] != "warning" {
		t.Fatal("missing log")
	}
	if _, ok := fileValue(t, got, "02_api.json")["api"]; !ok {
		t.Fatal("missing active api")
	}
	in.API.Local[0].Content = json.RawMessage(`null`)
	catalogFailure(t, in, "invalid_section_shape")
}

func TestCatalogOptionalSections(t *testing.T) {
	in := emptyCatalog()
	stats := selectionInput()
	in.Stats = &stats
	fake := selectionInput()
	in.FakeDNS = &fake
	dns := selectionInput()
	localCatalog(&dns, selectionID, "/dns/0", `{"servers":["192.0.2.53"]}`)
	future := selectionInput()
	localCatalog(&future, selectionLocalID, "/future/0", `[9007199254740993,false,null]`)
	in.Extra = []c.CatalogSectionInput{{Name: "dns", Selection: dns}, {Name: "futureSection", Selection: future}, {Name: "optional", Selection: selectionInput()}}
	got := catalog(t, in)
	if len(got.Files) != 6 || fileValue(t, got, "02_api.json")["stats"] == nil {
		t.Fatal("optional missing")
	}
	extra := fileValue(t, got, "06_extra.json")
	if !reflect.DeepEqual(extra["futureSection"], []any{json.Number("9007199254740993"), false, nil}) || len(extra["fakedns"].([]any)) != 0 {
		t.Fatalf("lost types: %+v", extra)
	}
	if _, ok := extra["optional"]; !ok {
		t.Fatal("empty optional missing")
	}
	sharedCatalog(&in.Extra[2].Selection, "550e8400-e29b-41d4-a716-446655440012", "/optional/0", `{`)
	in.Extra[2].Selection.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: in.Template.ID, ElementID: "550e8400-e29b-41d4-a716-446655440012"}, Position: "/exclude/0"}}
	if _, ok := fileValue(t, catalog(t, in), "06_extra.json")["optional"]; ok {
		t.Fatal("excluded optional remains")
	}
}

func TestCatalogSectionAssignments(t *testing.T) {
	for _, name := range []string{"log", "api", "inbounds", "outbounds", "routing", "rules", "stats", "fakedns", "exclude_ips", "", "duplicate", "bad\xff"} {
		t.Run(name, func(t *testing.T) {
			in := emptyCatalog()
			in.Extra = []c.CatalogSectionInput{{Name: name, Selection: selectionInput()}}
			code := "duplicate_catalog_section"
			if name == "exclude_ips" || name == "" || name == "bad\xff" {
				code = "invalid_catalog_record"
			}
			if name == "duplicate" {
				in.Extra = append(in.Extra, in.Extra[0])
			}
			catalogFailure(t, in, code)
		})
	}
}

func TestCatalogDeterminismIndependenceAndLateError(t *testing.T) {
	in := emptyCatalog()
	sharedCatalog(in.Outbounds, selectionID, "/outbounds/0", `{"tag":"direct","extension":{"values":[9007199254740993,false,{"mode":"custom"}]}}`)
	first, second := catalog(t, in), catalog(t, in)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("nondeterministic result")
	}
	values := fileValue(t, first, "04_outbounds.json")["outbounds"].([]any)[0].(map[string]any)["extension"].(map[string]any)["values"]
	if !reflect.DeepEqual(values, []any{json.Number("9007199254740993"), false, map[string]any{"mode": "custom"}}) {
		t.Fatal("unknown nested types or order lost", values)
	}
	for _, f := range first.Files {
		if f.Content[len(f.Content)-1] != '\n' || bytes.Contains(f.Content, []byte("\r")) {
			t.Fatal("bad newline")
		}
	}
	if len(first.Elements) != 1 || first.Elements[0].File != "04_outbounds.json" || first.Elements[0].Path != "/outbounds/0" || first.Elements[0].Source == nil {
		t.Fatalf("missing provenance: %+v", first.Elements)
	}
	first.Files[3].Content[0] = '!'
	first.Elements[0].Source.TemplateID = "changed"
	first.Elements[0].Location.Owner.ID = "changed"
	if !reflect.DeepEqual(second, catalog(t, in)) || in.Template.ID != "template-a" {
		t.Fatal("shared result or input")
	}
	bad := selectionInput()
	localCatalog(&bad, selectionLocalID, "/dns/0", `null`)
	in.Extra = []c.CatalogSectionInput{{Name: "dns", Selection: bad}}
	catalogFailure(t, in, "invalid_section_shape")
}

func TestCatalogEffectiveAndInactiveContent(t *testing.T) {
	in := emptyCatalog()
	sharedCatalog(in.Outbounds, selectionID, "/outbounds/0", `{"tag":"${tag}","old":true}`)
	in.Outbounds.Shared[0].Element.Variables = []c.VariableDefinition{{Name: "tag", Type: "string"}}
	in.Outbounds.Overrides = []c.LocatedContentOverride{{Position: "/replacement/0", Override: c.ContentOverride{Source: c.ElementSource{TemplateID: in.Template.ID, ElementID: selectionID}, Mode: c.ContentModeReplacement, Content: json.RawMessage(`{"tag":"replacement","literal":"${literal}","enabled":false,"extension":null}`)}}}
	localCatalog(in.Outbounds, selectionLocalID, "/outbounds/local", `{"tag":"local","literal":"${literal}"}`)
	sharedCatalog(in.Inbounds, "550e8400-e29b-41d4-a716-446655440012", "/inbounds/0", `{"tag":"${required}"}`)
	in.Inbounds.Shared[0].Element.Variables = []c.VariableDefinition{{Name: "required", Type: "string"}}
	in.DisabledInbounds = []c.ElementRef{{ID: in.Inbounds.Shared[0].Element.ID, Position: "/disabled/inbound"}}
	sharedCatalog(in.Rules, "550e8400-e29b-41d4-a716-446655440013", "/rules/0", `{"domain":"${required}"}`)
	in.Rules.Shared[0].Element.Variables = in.Inbounds.Shared[0].Element.Variables
	in.TemplateOrder = []string{in.Rules.Shared[0].Element.ID}
	in.DisabledRules = []c.ElementRef{{ID: in.TemplateOrder[0], Position: "/disabled/rule"}}
	sharedCatalog(in.Log, "550e8400-e29b-41d4-a716-446655440014", "/log/0", `{`)
	in.Log.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: in.Template.ID, ElementID: in.Log.Shared[0].Element.ID}, Position: "/excluded/log"}}
	got := catalog(t, in)
	items := fileValue(t, got, "04_outbounds.json")["outbounds"].([]any)
	if len(items) != 2 {
		t.Fatal(items)
	}
	replacement := items[0].(map[string]any)
	if _, ok := replacement["old"]; ok || replacement["literal"] != "${literal}" || replacement["enabled"] != false {
		t.Fatal(replacement)
	}
	if len(fileValue(t, got, "03_inbounds.json")["inbounds"].([]any)) != 0 {
		t.Fatal("disabled inbound emitted")
	}
	in.DisabledRules = nil
	catalogFailure(t, in, "missing_value")
}

func TestCatalogInheritedLiteralAndNull(t *testing.T) {
	in := emptyCatalog()
	sharedCatalog(in.Outbounds, selectionID, "/outbounds/0", `{"tag":"${name}","revision":2}`)
	in.Outbounds.Shared[0].Element.Variables = []c.VariableDefinition{{Name: "name", Type: "string", Default: json.RawMessage(`"${literal}"`)}}
	got := fileValue(t, catalog(t, in), "04_outbounds.json")["outbounds"].([]any)[0].(map[string]any)
	if got["tag"] != "${literal}" || got["revision"] != json.Number("2") {
		t.Fatal(got)
	}
	localCatalog(in.Inbounds, selectionLocalID, "/inbounds/0", `null`)
	catalogFailure(t, in, "invalid_section_shape")
	in.Inbounds.Local[0].Content = json.RawMessage(`{"extension":null}`)
	catalog(t, in)
}

func TestCatalogRuleOrderAndSettings(t *testing.T) {
	in := emptyCatalog()
	a := selectionID
	b := selectionLocalID
	cc := "550e8400-e29b-41d4-a716-446655440012"
	l := "550e8400-e29b-41d4-a716-446655440013"
	for _, item := range []struct{ id, tag string }{{a, "A"}, {b, "B"}, {cc, "C"}} {
		sharedCatalog(in.Rules, item.id, "/rules/"+item.tag, `{"ruleTag":"`+item.tag+`"}`)
	}
	localCatalog(in.Rules, l, "/rules/L", `{"ruleTag":"L"}`)
	in.TemplateOrder = []string{a, b, cc}
	in.PersonalOrder = []string{cc, l, a, b}
	in.Rules.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: in.Template.ID, ElementID: a}, Position: "/excluded/A"}}
	in.DisabledRules = []c.ElementRef{{ID: cc, Position: "/disabled/C"}}
	localCatalog(in.Routing, "550e8400-e29b-41d4-a716-446655440014", "/routing/0", `{"domainStrategy":"AsIs","extension":{"flag":true}}`)
	got := fileValue(t, catalog(t, in), "05_routing.json")["routing"].(map[string]any)
	rules := got["rules"].([]any)
	if len(rules) != 2 || rules[0].(map[string]any)["ruleTag"] != "L" || rules[1].(map[string]any)["ruleTag"] != "B" || got["domainStrategy"] != "AsIs" {
		t.Fatal(got)
	}
	in.PersonalOrder = []string{l, b, cc}
	catalogFailure(t, in, "order_element_not_found")
	in = emptyCatalog()
	localCatalog(in.Routing, selectionID, "/routing/0", `{"rules":[],"domainStrategy":"AsIs"}`)
	issue := catalogFailure(t, in, "invalid_catalog_record")
	if issue.Path == nil || *issue.Path != "/rules" {
		t.Fatal(issue)
	}
}

func TestCatalogOutboundAndFakeDNSSequence(t *testing.T) {
	in := emptyCatalog()
	sharedCatalog(in.Outbounds, selectionLocalID, "/outbounds/B", `{"tag":"B"}`)
	sharedCatalog(in.Outbounds, selectionID, "/outbounds/A", `{"tag":"A"}`)
	localCatalog(in.Outbounds, "550e8400-e29b-41d4-a716-446655440012", "/outbounds/L", `{"tag":"L"}`)
	fake := selectionInput()
	in.FakeDNS = &fake
	localCatalog(&fake, "550e8400-e29b-41d4-a716-446655440013", "/fakedns/0", `{"ipPool":"198.18.0.0/16"}`)
	localCatalog(&fake, "550e8400-e29b-41d4-a716-446655440014", "/fakedns/1", `{"ipPool":"198.19.0.0/16"}`)
	got := catalog(t, in)
	items := fileValue(t, got, "04_outbounds.json")["outbounds"].([]any)
	for i, tag := range []string{"B", "A", "L"} {
		if items[i].(map[string]any)["tag"] != tag {
			t.Fatal(items)
		}
	}
	items = fileValue(t, got, "06_extra.json")["fakedns"].([]any)
	if items[0].(map[string]any)["ipPool"] != "198.18.0.0/16" || items[1].(map[string]any)["ipPool"] != "198.19.0.0/16" {
		t.Fatal(items)
	}
}

func dependencyCatalog() c.CatalogInput {
	in := emptyCatalog()
	localCatalog(in.Rules, selectionID, "/rules/0", `{"inboundTag":["socks"],"outboundTag":"direct"}`)
	in.PersonalOrder = []string{selectionID}
	localCatalog(in.Inbounds, selectionLocalID, "/inbounds/0", `{"tag":"socks"}`)
	localCatalog(in.Outbounds, "550e8400-e29b-41d4-a716-446655440012", "/outbounds/0", `{"tag":"direct"}`)
	return in
}

func TestCatalogDependenciesBlockAndRelease(t *testing.T) {
	for _, kind := range []string{"outbound", "inbound", "disabled inbound"} {
		t.Run(kind, func(t *testing.T) {
			in := dependencyCatalog()
			switch kind {
			case "outbound":
				in.Outbounds.Local = nil
			case "inbound":
				in.Inbounds.Local = nil
			case "disabled inbound":
				in.DisabledInbounds = []c.ElementRef{{ID: selectionLocalID, Position: "/disabled/0"}}
			}
			issue := catalogFailure(t, in, "unresolved_dependency")
			if issue.ElementID != selectionID || issue.Path == nil || issue.Kind == "" {
				t.Fatal(issue)
			}
			in.DisabledRules = []c.ElementRef{{ID: selectionID, Position: "/disabled/rule"}}
			catalog(t, in)
		})
	}
	got := catalog(t, dependencyCatalog())
	if len(got.DependencyFindings) != 2 {
		t.Fatalf("missing findings: %+v", got.DependencyFindings)
	}
}

func TestCatalogDependencyShapesAndAmbiguity(t *testing.T) {
	for _, tc := range []struct{ kind, code string }{{"inbound", "ambiguous_inbound_tag"}, {"outbound", "ambiguous_outbound_tag"}, {"bad inbound", "invalid_inbound_tag"}, {"bad outbound", "invalid_outbound_tag"}, {"bad rule", "invalid_outbound_tag"}} {
		t.Run(tc.kind, func(t *testing.T) {
			in := dependencyCatalog()
			switch tc.kind {
			case "inbound":
				localCatalog(in.Inbounds, "550e8400-e29b-41d4-a716-446655440015", "/inbounds/1", `{"tag":"socks"}`)
			case "outbound":
				localCatalog(in.Outbounds, "550e8400-e29b-41d4-a716-446655440015", "/outbounds/1", `{"tag":"direct"}`)
			case "bad inbound":
				in.Inbounds.Local[0].Content = json.RawMessage(`{"tag":false}`)
			case "bad outbound":
				in.Outbounds.Local[0].Content = json.RawMessage(`{"tag":false}`)
			case "bad rule":
				in.Rules.Local[0].Content = json.RawMessage(`{"outboundTag":false}`)
			}
			catalogFailure(t, in, tc.code)
		})
	}
}

func balancedCatalog() c.CatalogInput {
	in := dependencyCatalog()
	in.Rules.Local[0].Content = json.RawMessage(`{"balancerTag":"pool"}`)
	localCatalog(in.Routing, "550e8400-e29b-41d4-a716-446655440014", "/routing/0", `{"balancers":[{"tag":"pool","selector":["none"],"fallbackTag":"direct"},{"tag":"unreachable","selector":false}]}`)
	return in
}

func TestCatalogBalancerFallbackAndReachability(t *testing.T) {
	in := balancedCatalog()
	got := catalog(t, in)
	found := false
	for _, finding := range got.DependencyFindings {
		if finding.Kind == "selector" && finding.Status == "no_matches" {
			found = true
		}
	}
	if !found {
		t.Fatal("nonblocking finding lost")
	}
	items := fileValue(t, got, "05_routing.json")["routing"].(map[string]any)["balancers"].([]any)
	if len(items) != 2 {
		t.Fatal("unreachable content lost")
	}
	in.Routing.Local[0].Content = json.RawMessage(`{"balancers":[{"tag":"pool","selector":["direct"],"fallbackTag":"missing"}]}`)
	issue := catalogFailure(t, in, "unresolved_dependency")
	if issue.Kind != "fallback" {
		t.Fatal(issue)
	}
	in.Routing.Local[0].Content = json.RawMessage(`{"balancers":[{"tag":"pool","selector":["none"]}]}`)
	issue = catalogFailure(t, in, "unresolved_dependency")
	if issue.Kind != "selector" {
		t.Fatal(issue)
	}
	in = balancedCatalog()
	in.Routing.Local[0].Content = json.RawMessage(`{"balancers":[{"tag":"pool","selector":["none","direct"]}]}`)
	catalog(t, in)
	in = balancedCatalog()
	localCatalog(in.Rules, "550e8400-e29b-41d4-a716-446655440015", "/rules/1", `{"outboundTag":"missing"}`)
	in.PersonalOrder = append(in.PersonalOrder, in.Rules.Local[1].ID)
	failed, issues := c.AssembleXrayCatalog(in)
	if failed != nil || len(issues) != 1 || issues[0].Kind != "direct" {
		t.Fatalf("working fallback falsely blocks: %+v", issues)
	}
}

func TestCatalogAlwaysAssemblesRouting(t *testing.T) {
	in := dependencyCatalog()
	sharedCatalog(in.Routing, "550e8400-e29b-41d4-a716-446655440014", "/routing/0", `{"domainStrategy":"${strategy}"}`)
	in.Routing.Shared[0].Element.Variables = []c.VariableDefinition{{Name: "strategy", Type: "string"}}
	catalogFailure(t, in, "missing_value")
}

func TestCatalogIdentityOriginsAndChildren(t *testing.T) {
	in := dependencyCatalog()
	in.Inbounds.Local = nil
	sharedCatalog(in.Inbounds, selectionLocalID, "/inbounds/shared", `"${object}"`)
	in.Inbounds.Shared[0].Element.Variables = []c.VariableDefinition{{Name: "object", Type: "object", Default: json.RawMessage(`{"tag":"socks"}`)}}
	outID := in.Outbounds.Local[0].ID
	in.Outbounds.Local = nil
	sharedCatalog(in.Outbounds, outID, "/outbounds/shared", `{"tag":"old"}`)
	in.Outbounds.Overrides = []c.LocatedContentOverride{{Position: "/replacement/0", Override: c.ContentOverride{Source: c.ElementSource{TemplateID: in.Template.ID, ElementID: outID}, Mode: c.ContentModeReplacement, Content: json.RawMessage(`{"tag":"direct"}`)}}}
	dns := selectionInput()
	localCatalog(&dns, "550e8400-e29b-41d4-a716-446655440014", "/dns/0", `{"hosts":{"example.com":"192.0.2.1"},"servers":[{"address":"192.0.2.53"}]}`)
	in.Extra = []c.CatalogSectionInput{{Name: "dns", Selection: dns}}
	localCatalog(in.Routing, "550e8400-e29b-41d4-a716-446655440015", "/routing/0", `{"balancers":[{"tag":"unreachable","selector":["none"]}]}`)
	got := catalog(t, in)
	for _, element := range got.Elements {
		root := fileValue(t, got, element.File)
		var value map[string]any
		switch element.File {
		case "03_inbounds.json":
			value = root["inbounds"].([]any)[0].(map[string]any)
		case "04_outbounds.json":
			value = root["outbounds"].([]any)[0].(map[string]any)
		case "05_routing.json":
			value = root["routing"].(map[string]any)
			if element.Path != "/routing" {
				value = value["rules"].([]any)[0].(map[string]any)
			}
		case "06_extra.json":
			value = root["dns"].(map[string]any)
		}
		if value["_xkeenUiId"] != element.ElementID {
			t.Fatalf("missing identity/provenance: %+v %+v", element, value)
		}
	}
	if bytes.Count(got.Files[5].Content, []byte(`_xkeenUiId`)) != 1 || bytes.Count(got.Files[4].Content, []byte(`_xkeenUiId`)) != 2 {
		t.Fatal("recursive marker")
	}
	if bytes.Contains(got.Files[0].Content, []byte(`_xkeenUiId`)) {
		t.Fatal("empty container marked")
	}
	in.Outbounds.Overrides[0].Override.Content = json.RawMessage(`{"tag":"direct","_xkeenUiId":false}`)
	issue := catalogFailure(t, in, "identity_marker_conflict")
	if issue.Path == nil || *issue.Path != "/_xkeenUiId" {
		t.Fatal(issue)
	}
}

func TestCatalogIdentityFakeDNSAndUnsupported(t *testing.T) {
	in := emptyCatalog()
	fake := selectionInput()
	in.FakeDNS = &fake
	localCatalog(&fake, selectionID, "/pool/0", `{}`)
	localCatalog(&fake, selectionLocalID, "/pool/1", `{}`)
	localCatalog(in.API, "550e8400-e29b-41d4-a716-446655440012", "/api/0", `{"_xkeenUiId":false}`)
	got := catalog(t, in)
	if bytes.Count(got.Files[5].Content, []byte(`_xkeenUiId`)) != 2 {
		t.Fatal("pools unmarked")
	}
	if fileValue(t, got, "02_api.json")["api"].(map[string]any)["_xkeenUiId"] != false {
		t.Fatal("API marker changed")
	}
}

func TestCatalogUnknownSectionNamesDoNotSelectIdentityPositions(t *testing.T) {
	for _, name := range []string{"inbound", "outbound", "rule", "hosts", "servers"} {
		t.Run(name, func(t *testing.T) {
			in := emptyCatalog()
			g := selectionInput()
			localCatalog(&g, selectionID, "/future/0", `{"_xkeenUiId":false}`)
			in.Extra = []c.CatalogSectionInput{{Name: name, Selection: g}}
			got := fileValue(t, catalog(t, in), "06_extra.json")
			if got[name].(map[string]any)["_xkeenUiId"] != false {
				t.Fatal("unknown section interpreted as element position")
			}
		})
	}
}

func TestCatalogSingletonMetadataBeforeContent(t *testing.T) {
	in := emptyCatalog()
	localCatalog(in.Log, selectionID, "/log/0", `{`)
	localCatalog(in.API, selectionLocalID, "/api/0", `{}`)
	localCatalog(in.API, "550e8400-e29b-41d4-a716-446655440012", "/api/1", `{}`)
	catalogFailure(t, in, "duplicate_catalog_section")
}

func TestCatalogObjectVariableConflictAndFindingIndependence(t *testing.T) {
	in := dependencyCatalog()
	in.Outbounds.Local = nil
	sharedCatalog(in.Outbounds, "550e8400-e29b-41d4-a716-446655440012", "/outbounds/shared", `"${object}"`)
	in.Outbounds.Shared[0].Element.Variables = []c.VariableDefinition{{Name: "object", Type: "object", Default: json.RawMessage(`{"tag":"direct","_xkeenUiId":false}`)}}
	before := bytes.Clone(in.Outbounds.Shared[0].Element.Variables[0].Default)
	issue := catalogFailure(t, in, "identity_marker_conflict")
	if issue.Path == nil || *issue.Path != "/_xkeenUiId" || !bytes.Equal(before, in.Outbounds.Shared[0].Element.Variables[0].Default) {
		t.Fatal(issue)
	}
	in.Outbounds.Shared[0].Element.Variables[0].Default = json.RawMessage(`{"tag":"direct"}`)
	first, second := catalog(t, in), catalog(t, in)
	first.DependencyFindings[0].ProviderIDs[0] = "changed"
	first.DependencyFindings[0].Location.Owner.ID = "changed"
	if !reflect.DeepEqual(second, catalog(t, in)) {
		t.Fatal("finding shares input or another result")
	}
}

func TestCatalogHiddenBindingsAndDisabledRecords(t *testing.T) {
	for _, tc := range []struct{ kind, code string }{{"mode", "invalid_element_record"}, {"override", "duplicate_override"}, {"disabled", "duplicate_disabled_rule"}, {"missing disabled", "disabled_element_not_found"}, {"invalid disabled", "invalid_element_id"}} {
		t.Run(tc.kind, func(t *testing.T) {
			in := emptyCatalog()
			sharedCatalog(in.Rules, selectionID, "/rules/0", `{`)
			in.TemplateOrder = []string{selectionID}
			source := c.ElementSource{TemplateID: in.Template.ID, ElementID: selectionID}
			in.Rules.Exclusions = []c.SharedExclusion{{Source: source, Position: "/exclusion/0"}}
			switch tc.kind {
			case "mode":
				in.Rules.Overrides = []c.LocatedContentOverride{{Position: "/override/0", Override: c.ContentOverride{Source: source, Mode: "invalid"}}}
			case "override":
				item := c.LocatedContentOverride{Position: "/override/0", Override: c.ContentOverride{Source: source, Mode: c.ContentModeVariables}}
				in.Rules.Overrides = []c.LocatedContentOverride{item, item}
			case "disabled":
				in.DisabledRules = []c.ElementRef{{ID: selectionID, Position: "/disabled/0"}, {ID: selectionID, Position: "/disabled/1"}}
			case "missing disabled":
				in.DisabledRules = []c.ElementRef{{ID: selectionLocalID, Position: "/disabled/0"}}
			case "invalid disabled":
				in.DisabledRules = []c.ElementRef{{ID: "private-invalid-id", Position: "/disabled/0"}}
			}
			issue := catalogFailure(t, in, tc.code)
			if tc.kind == "invalid disabled" && issue.ElementID != "" {
				t.Fatal("unsafe id")
			}
		})
	}
}

func TestCatalogAllHiddenAndKnownShapes(t *testing.T) {
	in := emptyCatalog()
	sharedCatalog(in.Rules, selectionID, "/rules/0", `{`)
	in.TemplateOrder = []string{selectionID}
	in.Rules.Exclusions = []c.SharedExclusion{{Source: c.ElementSource{TemplateID: in.Template.ID, ElementID: selectionID}, Position: "/excluded/0"}}
	localCatalog(in.Routing, selectionLocalID, "/routing/0", `{"domainStrategy":"AsIs","extension":{"flag":true}}`)
	got := fileValue(t, catalog(t, in), "05_routing.json")["routing"].(map[string]any)
	if len(got["rules"].([]any)) != 0 || got["domainStrategy"] != "AsIs" || got["extension"].(map[string]any)["flag"] != true {
		t.Fatal(got)
	}
	for _, name := range []string{"log", "api", "inbounds", "outbounds", "routing", "rules", "stats", "fakedns", "dns", "policy", "observatory"} {
		t.Run(name, func(t *testing.T) {
			in := emptyCatalog()
			g := selectionInput()
			localCatalog(&g, selectionID, "/"+name+"/0", `[]`)
			switch name {
			case "log":
				in.Log = &g
			case "api":
				in.API = &g
			case "inbounds":
				in.Inbounds = &g
			case "outbounds":
				in.Outbounds = &g
			case "routing":
				in.Routing = &g
			case "rules":
				in.Rules = &g
				in.PersonalOrder = []string{selectionID}
			case "stats":
				in.Stats = &g
			case "fakedns":
				in.FakeDNS = &g
			default:
				in.Extra = []c.CatalogSectionInput{{Name: name, Selection: g}}
			}
			catalogFailure(t, in, "invalid_section_shape")
		})
	}
}
