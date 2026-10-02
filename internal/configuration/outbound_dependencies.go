package configuration

import (
	"sort"
	"strconv"
	"strings"
)

// OutboundSnapshot is one router's complete supplied state for outbound analysis.
type OutboundSnapshot struct {
	Rules, Outbounds, Routing    RouterSelectionInput
	TemplateOrder, PersonalOrder []string
	DisabledRules                []ElementRef
	OtherShared, OtherLocal      []ElementRef
}

type OutboundReference struct {
	RuleID      string
	ElementID   string
	Location    ElementLocation
	Path        string
	PartIndex   int
	Kind        string
	OutboundIDs []string
	Status      string
}

type OutboundAnalysis struct {
	FullRules  []RuleOrderEntry
	References []OutboundReference
	Ready      bool
}

type parsedOutgoingRule struct {
	reference OutboundReference
	tag       string
}

type outboundProvider struct {
	id       string
	location ElementLocation
	index    int
}

type nestedBalancer struct {
	parentID string
	location ElementLocation
	index    int
	fields   map[string]any
}

// AnalyzeOutboundDependencies evaluates active outgoing references in one router.
func AnalyzeOutboundDependencies(input OutboundSnapshot) (*OutboundAnalysis, []SelectionIssue) {
	if input.Rules.Template != input.Outbounds.Template || input.Rules.Router != input.Outbounds.Router ||
		input.Rules.Template != input.Routing.Template || input.Rules.Router != input.Routing.Router {
		return nil, invalidSelectionContext()
	}
	for _, section := range []RouterSelectionInput{input.Rules, input.Outbounds, input.Routing} {
		if issues := validateSelectionMetadata(section); len(issues) != 0 {
			return nil, issues
		}
	}
	shared := make([]ElementRef, 0)
	local := make([]ElementRef, 0)
	for _, section := range []RouterSelectionInput{input.Rules, input.Outbounds, input.Routing} {
		for _, record := range section.Shared {
			shared = append(shared, ElementRef{ID: record.Element.ID, Position: record.Position})
		}
		for _, record := range section.Local {
			local = append(local, ElementRef{ID: record.ID, Position: record.Position})
		}
	}
	shared = append(shared, input.OtherShared...)
	local = append(local, input.OtherLocal...)
	if issues := ValidateRouterLocalIDs(input.Rules.Template, shared, input.Rules.Router, local); len(issues) != 0 {
		return nil, selectionElementIssues(issues)
	}
	order, issues := AssembleRuleOrder(orderInputFromSelection(OrderedRouterInput{Selection: input.Rules, TemplateOrder: input.TemplateOrder, PersonalOrder: input.PersonalOrder}))
	if len(issues) != 0 {
		return nil, issues
	}
	disabled, issues := validateDisabledRefs(input.Rules.Router, input.DisabledRules, order.Full, "duplicate_disabled_rule")
	if len(issues) != 0 {
		return nil, issues
	}
	parsedRules := make([]parsedOutgoingRule, 0)
	for _, entry := range order.Visible {
		if _, found := disabled[entry.ID]; found {
			continue
		}
		effective, issues := selectSnapshotElement(input.Rules, entry.ID)
		if len(issues) != 0 {
			return nil, issues
		}
		parsed, issues := readOutgoingRule(entry.ID, effective)
		if len(issues) != 0 {
			return nil, issues
		}
		if parsed != nil {
			parsedRules = append(parsedRules, *parsed)
		}
	}
	balancers, issues := indexReachableBalancers(input.Routing, parsedRules)
	if len(issues) != 0 {
		return nil, issues
	}
	providers, issues := indexActiveOutbounds(input.Outbounds, parsedRules, balancers)
	if len(issues) != 0 {
		return nil, issues
	}
	return resolvePreparedOutbounds(order.Full, parsedRules, balancers, providers)
}

func resolvePreparedOutbounds(full []RuleOrderEntry, parsedRules []parsedOutgoingRule, balancers map[string]nestedBalancer, providers map[string]outboundProvider) (*OutboundAnalysis, []SelectionIssue) {
	references := make([]OutboundReference, 0, len(parsedRules))
	ready := true
	for _, parsed := range parsedRules {
		ref := parsed.reference
		if ref.Kind == "direct" {
			if provider, found := providers[parsed.tag]; found {
				ref.OutboundIDs = []string{provider.id}
				ref.Status = "resolved"
			} else {
				ref.Status = "missing"
				ready = false
			}
		} else {
			if balancer, found := balancers[parsed.tag]; found {
				ref.Status = "resolved"
				references = append(references, ref)
				nested, nestedReady, issues := resolveBalancer(parsed.reference.RuleID, balancer, providers)
				if len(issues) != 0 {
					return nil, issues
				}
				references = append(references, nested...)
				if !nestedReady {
					ready = false
				}
				continue
			} else {
				ref.Status = "missing"
				ready = false
			}
		}
		references = append(references, ref)
	}
	return &OutboundAnalysis{FullRules: full, References: references, Ready: ready}, nil
}

func readOutgoingRule(id string, effective EffectiveElement) (*parsedOutgoingRule, []SelectionIssue) {
	value, issue := parseJSON(effective.Content, Issue{ElementID: id, Source: "content"})
	if issue != nil {
		return nil, []SelectionIssue{{Issue: *issue, Location: effective.Location}}
	}
	obj, ok := value.(map[string]any)
	if !ok {
		path := ""
		return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_rule_shape", ElementID: id, Source: "content", Path: &path}, Location: effective.Location}}
	}
	var direct, balancer string
	if raw, exists := obj["outboundTag"]; exists {
		var valid bool
		direct, valid = raw.(string)
		if !valid {
			path := "/outboundTag"
			return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_outbound_tag", ElementID: id, Source: "content", Path: &path}, Location: effective.Location}}
		}
	}
	if raw, exists := obj["balancerTag"]; exists {
		var valid bool
		balancer, valid = raw.(string)
		if !valid {
			path := "/balancerTag"
			return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_balancer_tag", ElementID: id, Source: "content", Path: &path}, Location: effective.Location}}
		}
	}
	if direct == "" && balancer == "" {
		return nil, nil
	}
	ref := OutboundReference{RuleID: id, ElementID: id, Location: effective.Location}
	tag := direct
	if direct != "" {
		ref.Path = "/outboundTag"
		ref.Kind = "direct"
	} else {
		ref.Path = "/balancerTag"
		ref.Kind = "balancer"
		tag = balancer
	}
	return &parsedOutgoingRule{reference: ref, tag: tag}, nil
}

func indexActiveOutbounds(input RouterSelectionInput, refs []parsedOutgoingRule, balancers map[string]nestedBalancer) (map[string]outboundProvider, []SelectionIssue) {
	return indexSelectedOutbounds(input, refs, balancers, selectSnapshotElement)
}

func indexSelectedOutbounds(input RouterSelectionInput, refs []parsedOutgoingRule, balancers map[string]nestedBalancer, selectElement func(RouterSelectionInput, string) (EffectiveElement, []SelectionIssue)) (map[string]outboundProvider, []SelectionIssue) {
	excluded := make(map[string]struct{}, len(input.Exclusions))
	for _, exclusion := range input.Exclusions {
		excluded[exclusion.Source.ElementID] = struct{}{}
	}
	providers := make(map[string]outboundProvider)
	for index, id := range allOutboundIDs(input) {
		if _, found := excluded[id]; found {
			continue
		}
		effective, issues := selectElement(input, id)
		if len(issues) != 0 {
			return nil, issues
		}
		value, issue := parseJSON(effective.Content, Issue{ElementID: id, Source: "content"})
		if issue != nil {
			return nil, []SelectionIssue{{Issue: *issue, Location: effective.Location}}
		}
		obj, ok := value.(map[string]any)
		if !ok {
			path := ""
			return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_outbound_shape", ElementID: id, Source: "content", Path: &path}, Location: effective.Location}}
		}
		raw, present := obj["tag"]
		if !present {
			continue
		}
		tag, ok := raw.(string)
		if !ok {
			path := "/tag"
			return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_outbound_tag", ElementID: id, Source: "content", Path: &path}, Location: effective.Location}}
		}
		if first, found := providers[tag]; found {
			path := "/tag"
			issues := []SelectionIssue{{Issue: Issue{Code: "ambiguous_outbound_tag", ElementID: id, Source: "content", Path: &path}, Location: effective.Location, OtherLocation: &first.location}}
			for _, parsed := range refs {
				if parsed.reference.Kind == "direct" {
					if parsed.tag == tag {
						refPath := parsed.reference.Path
						issues = append(issues, SelectionIssue{Issue: Issue{Code: "ambiguous_outbound_reference", ElementID: parsed.reference.RuleID, Source: "content", Path: &refPath}, Location: parsed.reference.Location})
					}
					continue
				}
				balancer, found := balancers[parsed.tag]
				if !found {
					continue
				}
				nested := ambiguousBalancerOutboundReferences(tag, balancer)
				if len(nested) == 0 {
					continue
				}
				issues = append(issues, nested...)
				refPath := parsed.reference.Path
				issues = append(issues, SelectionIssue{Issue: Issue{Code: "ambiguous_balancer_reference", ElementID: parsed.reference.RuleID, Source: "content", Path: &refPath}, Location: parsed.reference.Location})
			}
			return nil, issues
		}
		providers[tag] = outboundProvider{id: id, location: effective.Location, index: index}
	}
	return providers, nil
}

func ambiguousBalancerOutboundReferences(tag string, balancer nestedBalancer) []SelectionIssue {
	base := "/balancers/" + strconv.Itoa(balancer.index)
	issues := make([]SelectionIssue, 0)
	add := func(path string) {
		issues = append(issues, SelectionIssue{Issue: Issue{Code: "ambiguous_outbound_reference", ElementID: balancer.parentID, Source: "content", Path: &path}, Location: balancer.location})
	}
	switch selector := balancer.fields["selector"].(type) {
	case string:
		for _, prefix := range strings.Split(selector, ",") {
			if strings.HasPrefix(tag, prefix) {
				add(base + "/selector")
			}
		}
	case []any:
		for i, raw := range selector {
			if prefix, ok := raw.(string); ok && strings.HasPrefix(tag, prefix) {
				add(base + "/selector/" + strconv.Itoa(i))
			}
		}
	}
	if fallback, ok := balancer.fields["fallbackTag"].(string); ok && fallback == tag {
		add(base + "/fallbackTag")
	}
	return issues
}

func allOutboundIDs(input RouterSelectionInput) []string {
	ids := make([]string, 0, len(input.Shared)+len(input.Local))
	for _, record := range input.Shared {
		ids = append(ids, record.Element.ID)
	}
	for _, record := range input.Local {
		ids = append(ids, record.ID)
	}
	return ids
}

func resolveBalancer(ruleID string, balancer nestedBalancer, providers map[string]outboundProvider) ([]OutboundReference, bool, []SelectionIssue) {
	base := "/balancers/" + strconv.Itoa(balancer.index)
	issueAt := func(code, path string) []SelectionIssue {
		return []SelectionIssue{{Issue: Issue{Code: code, ElementID: balancer.parentID, Source: "content", Path: &path}, Location: balancer.location}}
	}
	raw, found := balancer.fields["selector"]
	if !found {
		return nil, false, issueAt("invalid_selector", base+"/selector")
	}
	var prefixes []string
	array := false
	switch values := raw.(type) {
	case string:
		if values == "" {
			return nil, false, issueAt("invalid_selector", base+"/selector")
		}
		prefixes = strings.Split(values, ",")
	case []any:
		array = true
		if len(values) == 0 {
			return nil, false, issueAt("invalid_selector", base+"/selector")
		}
		for i, value := range values {
			prefix, ok := value.(string)
			if !ok {
				return nil, false, issueAt("invalid_selector", base+"/selector/"+strconv.Itoa(i))
			}
			prefixes = append(prefixes, prefix)
		}
	default:
		return nil, false, issueAt("invalid_selector", base+"/selector")
	}
	fallback, hasFallback := balancer.fields["fallbackTag"]
	var fallbackTag string
	if hasFallback {
		var ok bool
		fallbackTag, ok = fallback.(string)
		if !ok {
			return nil, false, issueAt("invalid_fallback_tag", base+"/fallbackTag")
		}
	}
	references := make([]OutboundReference, 0, len(prefixes)+1)
	totalMatches := 0
	for i, prefix := range prefixes {
		path := base + "/selector"
		if array {
			path += "/" + strconv.Itoa(i)
		}
		ids := matchOutboundPrefix(providers, prefix)
		ref := OutboundReference{RuleID: ruleID, ElementID: balancer.parentID, Location: balancer.location, Path: path, PartIndex: i, Kind: "selector", OutboundIDs: ids, Status: "resolved"}
		if len(ids) == 0 {
			ref.Status = "no_matches"
		}
		totalMatches += len(ids)
		references = append(references, ref)
	}
	fallbackReady := false
	if hasFallback {
		ref := OutboundReference{RuleID: ruleID, ElementID: balancer.parentID, Location: balancer.location, Path: base + "/fallbackTag", Kind: "fallback"}
		if provider, found := providers[fallbackTag]; found {
			ref.OutboundIDs = []string{provider.id}
			ref.Status = "resolved"
			fallbackReady = true
		} else {
			ref.Status = "missing"
		}
		references = append(references, ref)
	}
	return references, (totalMatches > 0 || fallbackReady) && (!hasFallback || fallbackReady), nil
}

func matchOutboundPrefix(providers map[string]outboundProvider, prefix string) []string {
	matches := make([]outboundProvider, 0)
	for tag, provider := range providers {
		if strings.HasPrefix(tag, prefix) {
			matches = append(matches, provider)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].index < matches[j].index })
	ids := make([]string, len(matches))
	for i, provider := range matches {
		ids[i] = provider.id
	}
	return ids
}

func indexReachableBalancers(input RouterSelectionInput, refs []parsedOutgoingRule) (map[string]nestedBalancer, []SelectionIssue) {
	return indexSelectedBalancers(input, refs, selectSnapshotElement)
}

func indexSelectedBalancers(input RouterSelectionInput, refs []parsedOutgoingRule, selectElement func(RouterSelectionInput, string) (EffectiveElement, []SelectionIssue)) (map[string]nestedBalancer, []SelectionIssue) {
	requested := make(map[string][]parsedOutgoingRule)
	for _, ref := range refs {
		if ref.reference.Kind == "balancer" {
			requested[ref.tag] = append(requested[ref.tag], ref)
		}
	}
	result := make(map[string]nestedBalancer)
	if len(requested) == 0 {
		return result, nil
	}
	excluded := make(map[string]struct{}, len(input.Exclusions))
	for _, exclusion := range input.Exclusions {
		excluded[exclusion.Source.ElementID] = struct{}{}
	}
	for _, id := range allOutboundIDs(input) {
		if _, found := excluded[id]; found {
			continue
		}
		effective, issues := selectElement(input, id)
		if len(issues) != 0 {
			return nil, issues
		}
		value, issue := parseJSON(effective.Content, Issue{ElementID: id, Source: "content"})
		if issue != nil {
			return nil, []SelectionIssue{{Issue: *issue, Location: effective.Location}}
		}
		obj, ok := value.(map[string]any)
		if !ok {
			path := ""
			return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_routing_shape", ElementID: id, Source: "content", Path: &path}, Location: effective.Location}}
		}
		raw, present := obj["balancers"]
		if !present {
			continue
		}
		items, ok := raw.([]any)
		if !ok {
			path := "/balancers"
			return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_balancers", ElementID: id, Source: "content", Path: &path}, Location: effective.Location}}
		}
		for index, item := range items {
			fields, ok := item.(map[string]any)
			if !ok {
				continue
			}
			tag, ok := fields["tag"].(string)
			if !ok || len(requested[tag]) == 0 {
				continue
			}
			current := nestedBalancer{parentID: id, location: effective.Location, index: index, fields: fields}
			if first, found := result[tag]; found {
				firstPath := "/balancers/" + strconv.Itoa(first.index) + "/tag"
				secondPath := "/balancers/" + strconv.Itoa(index) + "/tag"
				issues := []SelectionIssue{
					{Issue: Issue{Code: "ambiguous_balancer_tag", ElementID: first.parentID, Source: "content", Path: &firstPath}, Location: first.location},
					{Issue: Issue{Code: "ambiguous_balancer_tag", ElementID: id, Source: "content", Path: &secondPath}, Location: effective.Location},
				}
				for _, ref := range requested[tag] {
					path := ref.reference.Path
					issues = append(issues, SelectionIssue{Issue: Issue{Code: "ambiguous_balancer_reference", ElementID: ref.reference.RuleID, Source: "content", Path: &path}, Location: ref.reference.Location})
				}
				return nil, issues
			}
			result[tag] = current
		}
	}
	return result, nil
}
