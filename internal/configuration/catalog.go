package configuration

import (
	"encoding/json"
	"strconv"
	"unicode/utf8"
)

// CatalogInput is a complete in-memory snapshot. Required group pointers distinguish
// missing data from an explicitly empty selection. No defaults are read elsewhere.
type CatalogInput struct {
	Template, Router                              Owner
	Log, API, Inbounds, Outbounds, Routing, Rules *RouterSelectionInput
	Stats, FakeDNS                                *RouterSelectionInput
	Extra                                         []CatalogSectionInput
	TemplateOrder, PersonalOrder                  []string
	DisabledRules, DisabledInbounds               []ElementRef
	OtherShared, OtherLocal                       []ElementRef
}

type CatalogSectionInput struct {
	Name      string
	Selection RouterSelectionInput
}
type CatalogFile struct {
	Name    string
	Content json.RawMessage
}
type CatalogElementLocation struct {
	ElementID, Origin string
	Source            *ElementSource
	Mode              ContentMode
	Location          ElementLocation
	File, Path        string
}
type CatalogFinding struct {
	ElementID, Kind, Status, Path string
	Location                      ElementLocation
	PartIndex                     int
	ProviderIDs                   []string
}
type XrayCatalog struct {
	Files              []CatalogFile
	Elements           []CatalogElementLocation
	DependencyFindings []CatalogFinding
}
type CatalogIssue struct {
	SelectionIssue
	Kind string
}

// AssembleXrayCatalog returns a complete independent candidate or safe issues.
// Success does not certify Xray validation, publication or application.
func AssembleXrayCatalog(input CatalogInput) (*XrayCatalog, []CatalogIssue) {
	groups := catalogGroups(input)
	for _, group := range groups {
		if group.input == nil && group.required {
			return nil, []CatalogIssue{catalogRecordIssue(input.Router, "/"+group.name, "missing_catalog_section")}
		}
	}
	if issues := validateCatalogMetadata(input, groups); len(issues) != 0 {
		return nil, issues
	}
	order, issues := AssembleRuleOrder(orderInputFromSelection(OrderedRouterInput{Selection: *input.Rules, TemplateOrder: input.TemplateOrder, PersonalOrder: input.PersonalOrder}))
	if len(issues) != 0 {
		return nil, catalogSelectionIssues(issues)
	}
	disabledRules, issues := validateDisabledRefs(input.Router, input.DisabledRules, order.Full, "duplicate_disabled_rule")
	if len(issues) != 0 {
		return nil, catalogSelectionIssues(issues)
	}
	disabledInbounds, issues := validateDisabledRefs(input.Router, input.DisabledInbounds, catalogKnownEntries(*input.Inbounds), "duplicate_disabled_inbound")
	if len(issues) != 0 {
		return nil, catalogSelectionIssues(issues)
	}
	prepared := make(map[string][]catalogElement)
	for _, group := range groups {
		var disabled map[string]struct{}
		if group.name == "rules" {
			disabled = disabledRules
		}
		if group.name == "inbounds" {
			disabled = disabledInbounds
		}
		var ids []string
		if group.input != nil {
			ids = activeCatalogIDs(*group.input)
		}
		if group.name == "rules" {
			ids = ruleOrderIDs(order.Visible)
		}
		elements, issues := prepareCatalogGroup(group, ids, disabled)
		if len(issues) != 0 {
			return nil, issues
		}
		prepared[group.name] = elements
	}
	findings, catalogIssues := analyzeCatalogDependencies(input, order.Full, prepared)
	if len(catalogIssues) != 0 {
		return nil, catalogIssues
	}
	if issues := projectCatalogIdentities(groups, prepared); len(issues) != 0 {
		return nil, issues
	}
	objects := []map[string]any{{"log": map[string]any{}}, {}, {"inbounds": []any{}}, {"outbounds": []any{}}, {"routing": map[string]any{"rules": []any{}}}}
	names := []string{"01_log.json", "02_api.json", "03_inbounds.json", "04_outbounds.json", "05_routing.json"}
	for index, name := range []string{"log", "api", "inbounds", "outbounds", "routing"} {
		elements := prepared[name]
		if name == "inbounds" || name == "outbounds" {
			values := []any{}
			for _, element := range elements {
				values = append(values, element.value)
			}
			objects[index][name] = values
		} else if len(elements) > 0 {
			objects[index][name] = elements[0].value
		}
	}
	rules := []any{}
	for _, element := range prepared["rules"] {
		rules = append(rules, element.value)
	}
	objects[4]["routing"].(map[string]any)["rules"] = rules
	if input.Stats != nil {
		if value, ok := optionalCatalogValue(*input.Stats, prepared["stats"], false); ok {
			objects[1]["stats"] = value
		}
	}
	extra := map[string]any{}
	for _, group := range groups[6:] {
		if group.name == "stats" || group.input == nil {
			continue
		}
		if value, ok := optionalCatalogValue(*group.input, prepared[group.name], group.collection); ok {
			extra[group.name] = value
		}
	}
	if len(extra) > 0 {
		objects = append(objects, extra)
		names = append(names, "06_extra.json")
	}
	result := &XrayCatalog{Elements: catalogProvenance(groups, prepared), DependencyFindings: findings}
	for i, object := range objects {
		raw, _ := json.MarshalIndent(object, "", "  ")
		result.Files = append(result.Files, CatalogFile{Name: names[i], Content: append(raw, '\n')})
	}
	return result, nil
}

func projectCatalogIdentities(groups []catalogGroup, prepared map[string][]catalogElement) []CatalogIssue {
	for _, group := range groups {
		position := group.name
		switch position {
		case "inbounds":
			position = "inbound"
		case "outbounds":
			position = "outbound"
		case "rules":
			position = "rule"
		case "log", "dns", "routing", "policy", "stats", "observatory", "fakedns":
			// Supported whole sections and the dedicated FakeDNS pool collection.
		default:
			position = ""
		}
		for i := range prepared[group.name] {
			element := &prepared[group.name][i]
			raw, issues := ProjectElementIdentity(IdentityProjection{ElementID: element.id, Location: element.effective.Location, Position: position, Version: XrayIdentityVersion, Content: element.effective.Content})
			if len(issues) != 0 {
				return catalogSelectionIssues(issues)
			}
			value, issue := parseJSON(raw, Issue{ElementID: element.id, Source: "content"})
			if issue != nil {
				return catalogSelectionIssues([]SelectionIssue{{Issue: *issue, Location: element.effective.Location}})
			}
			element.value = value
		}
	}
	return nil
}

func catalogProvenance(groups []catalogGroup, prepared map[string][]catalogElement) []CatalogElementLocation {
	result := []CatalogElementLocation{}
	for _, group := range groups {
		file, path := catalogPlacement(group.name)
		for i, element := range prepared[group.name] {
			location := path
			if group.collection {
				location = appendPath(path, strconv.Itoa(i))
			}
			var source *ElementSource
			if element.effective.Source != nil {
				copy := *element.effective.Source
				source = &copy
			}
			result = append(result, CatalogElementLocation{ElementID: element.id, Origin: element.effective.Origin, Source: source, Mode: element.effective.Mode, Location: element.effective.Location, File: file, Path: location})
		}
	}
	return result
}
func catalogPlacement(name string) (string, string) {
	file := "06_extra.json"
	path := appendPath("", name)
	switch name {
	case "log":
		file = "01_log.json"
	case "api", "stats":
		file = "02_api.json"
	case "inbounds":
		file = "03_inbounds.json"
	case "outbounds":
		file = "04_outbounds.json"
	case "routing":
		file = "05_routing.json"
	case "rules":
		file = "05_routing.json"
		path = "/routing/rules"
	}
	return file, path
}

type catalogElement struct {
	id        string
	effective EffectiveElement
	value     any
}

func validateCatalogMetadata(input CatalogInput, groups []catalogGroup) []CatalogIssue {
	seen := map[string]ElementLocation{}
	for _, name := range []string{"log", "api", "inbounds", "outbounds", "routing", "rules", "stats", "fakedns"} {
		seen[name] = ElementLocation{Owner: input.Router, Position: "/" + name}
	}
	for i, section := range input.Extra {
		position := "/extra/" + strconv.Itoa(i)
		if section.Name == "" || section.Name == "exclude_ips" || !utf8.ValidString(section.Name) {
			return []CatalogIssue{catalogRecordIssue(input.Router, position, "invalid_catalog_record")}
		}
		if previous, ok := seen[section.Name]; ok {
			issue := catalogRecordIssue(input.Router, position, "duplicate_catalog_section")
			issue.OtherLocation = &previous
			return []CatalogIssue{issue}
		}
		seen[section.Name] = ElementLocation{Owner: input.Router, Position: position}
	}
	shared, local := append([]ElementRef(nil), input.OtherShared...), append([]ElementRef(nil), input.OtherLocal...)
	for _, group := range groups {
		if group.input == nil {
			continue
		}
		g := group.input
		if g.Template != input.Template || g.Router != input.Router {
			return catalogSelectionIssues(invalidSelectionContext())
		}
		if issues := validateSelectionMetadata(*g); len(issues) != 0 {
			return catalogSelectionIssues(issues)
		}
		for _, record := range g.Shared {
			shared = append(shared, ElementRef{ID: record.Element.ID, Position: record.Position})
		}
		for _, record := range g.Local {
			local = append(local, ElementRef{ID: record.ID, Position: record.Position})
		}
	}
	if issues := ValidateRouterLocalIDs(input.Template, shared, input.Router, local); len(issues) != 0 {
		return catalogSelectionIssues(selectionElementIssues(issues))
	}
	for _, group := range groups {
		if group.input == nil || group.collection {
			continue
		}
		ids := activeCatalogIDs(*group.input)
		if len(ids) > 1 {
			first := catalogInputLocation(*group.input, ids[0])
			second := catalogInputLocation(*group.input, ids[1])
			return catalogSingletonConflict(ids[1], first, second)
		}
	}
	return nil
}

func catalogSingletonConflict(id string, first, second ElementLocation) []CatalogIssue {
	return []CatalogIssue{{SelectionIssue: SelectionIssue{Issue: Issue{Code: "duplicate_catalog_section", Source: "record", ElementID: id}, Location: second, OtherLocation: &first}}}
}

func prepareCatalogGroup(group catalogGroup, ids []string, disabled map[string]struct{}) ([]catalogElement, []CatalogIssue) {
	if group.input == nil {
		return nil, nil
	}
	g := *group.input
	result := make([]catalogElement, 0, len(ids))
	for _, id := range ids {
		if _, found := disabled[id]; found {
			continue
		}
		effective, issues := selectSnapshotElement(g, id)
		if len(issues) != 0 {
			return nil, catalogSelectionIssues(issues)
		}
		value, issue := parseJSON(effective.Content, Issue{ElementID: id, Source: "content"})
		if issue != nil {
			return nil, catalogSelectionIssues([]SelectionIssue{{Issue: *issue, Location: effective.Location}})
		}
		if _, ok := value.(map[string]any); !ok && catalogObjectRequired(group.name) {
			path := ""
			return nil, catalogSelectionIssues([]SelectionIssue{{Issue: Issue{Code: "invalid_section_shape", ElementID: id, Source: "content", Path: &path}, Location: effective.Location}})
		}
		if group.name == "routing" {
			if _, present := value.(map[string]any)["rules"]; present {
				path := "/rules"
				return nil, catalogSelectionIssues([]SelectionIssue{{Issue: Issue{Code: "invalid_catalog_record", ElementID: id, Source: "content", Path: &path}, Location: effective.Location}})
			}
		}
		result = append(result, catalogElement{id: id, effective: effective, value: value})
	}
	return result, nil
}

func catalogKnownEntries(input RouterSelectionInput) []RuleOrderEntry {
	entries := []RuleOrderEntry{}
	for _, id := range allOutboundIDs(input) {
		entries = append(entries, RuleOrderEntry{ID: id})
	}
	return entries
}

func activeCatalogIDs(input RouterSelectionInput) []string {
	excluded := make(map[string]bool)
	for _, record := range input.Exclusions {
		excluded[record.Source.ElementID] = true
	}
	ids := []string{}
	for _, id := range allOutboundIDs(input) {
		if !excluded[id] {
			ids = append(ids, id)
		}
	}
	return ids
}
func catalogInputLocation(input RouterSelectionInput, id string) ElementLocation {
	for _, record := range input.Shared {
		if record.Element.ID == id {
			return ElementLocation{Owner: input.Template, Position: record.Position}
		}
	}
	for _, record := range input.Local {
		if record.ID == id {
			return ElementLocation{Owner: input.Router, Position: record.Position}
		}
	}
	return ElementLocation{}
}
func catalogSelectionIssues(issues []SelectionIssue) []CatalogIssue {
	if len(issues) == 0 {
		return nil
	}
	result := make([]CatalogIssue, len(issues))
	for i, issue := range issues {
		result[i] = CatalogIssue{SelectionIssue: issue}
	}
	return result
}

type catalogGroup struct {
	name                 string
	input                *RouterSelectionInput
	required, collection bool
}

func catalogGroups(input CatalogInput) []catalogGroup {
	groups := []catalogGroup{{"log", input.Log, true, false}, {"api", input.API, true, false}, {"inbounds", input.Inbounds, true, true}, {"outbounds", input.Outbounds, true, true}, {"routing", input.Routing, true, false}, {"rules", input.Rules, true, true}, {"stats", input.Stats, false, false}, {"fakedns", input.FakeDNS, false, true}}
	for i := range input.Extra {
		groups = append(groups, catalogGroup{name: input.Extra[i].Name, input: &input.Extra[i].Selection})
	}
	return groups
}

func catalogObjectRequired(name string) bool {
	switch name {
	case "log", "api", "inbounds", "outbounds", "routing", "rules", "stats", "dns", "policy", "observatory", "fakedns":
		return true
	}
	return false
}
func optionalCatalogValue(input RouterSelectionInput, elements []catalogElement, collection bool) (any, bool) {
	if collection {
		values := []any{}
		for _, e := range elements {
			values = append(values, e.value)
		}
		return values, true
	}
	if len(elements) > 0 {
		return elements[0].value, true
	}
	if len(input.Shared) == 0 && len(input.Local) == 0 {
		return map[string]any{}, true
	}
	return nil, false
}
func catalogRecordIssue(owner Owner, position, code string) CatalogIssue {
	return CatalogIssue{SelectionIssue: SelectionIssue{Issue: Issue{Code: code, Source: "record"}, Location: ElementLocation{Owner: owner, Position: position}}}
}
