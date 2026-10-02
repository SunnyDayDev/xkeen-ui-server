package configuration

// Analyze only the selected contents that will be emitted. The public B10/B11
// operations still prepare their own snapshots and share these analysis rules.
func analyzeCatalogDependencies(input CatalogInput, full []RuleOrderEntry, prepared map[string][]catalogElement) ([]CatalogFinding, []CatalogIssue) {
	rules := make([]OrderedRule, 0, len(prepared["rules"]))
	for _, element := range prepared["rules"] {
		rules = append(rules, OrderedRule{ID: element.id, Effective: element.effective})
	}
	inbounds := make(map[string]EffectiveElement)
	for _, element := range prepared["inbounds"] {
		inbounds[element.id] = element.effective
	}
	incoming, issues := analyzePreparedInbounds(*input.Inbounds, full, rules, inbounds)
	if len(issues) != 0 {
		return nil, catalogSelectionIssues(issues)
	}
	parsed := []parsedOutgoingRule{}
	for _, rule := range rules {
		ref, issues := readOutgoingRule(rule.ID, rule.Effective)
		if len(issues) != 0 {
			return nil, catalogSelectionIssues(issues)
		}
		if ref != nil {
			parsed = append(parsed, *ref)
		}
	}
	selected := make(map[string]EffectiveElement)
	for _, name := range []string{"routing", "outbounds"} {
		for _, element := range prepared[name] {
			selected[element.id] = element.effective
		}
	}
	selectElement := func(_ RouterSelectionInput, id string) (EffectiveElement, []SelectionIssue) { return selected[id], nil }
	balancers, issues := indexSelectedBalancers(*input.Routing, parsed, selectElement)
	if len(issues) != 0 {
		return nil, catalogSelectionIssues(issues)
	}
	providers, issues := indexSelectedOutbounds(*input.Outbounds, parsed, balancers, selectElement)
	if len(issues) != 0 {
		return nil, catalogSelectionIssues(issues)
	}
	outgoing, issues := resolvePreparedOutbounds(full, parsed, balancers, providers)
	if len(issues) != 0 {
		return nil, catalogSelectionIssues(issues)
	}
	findings := []CatalogFinding{}
	blocking := []CatalogIssue{}
	add := func(finding CatalogFinding, blocks bool) {
		findings = append(findings, finding)
		if blocks {
			path := finding.Path
			blocking = append(blocking, CatalogIssue{SelectionIssue: SelectionIssue{Issue: Issue{Code: "unresolved_dependency", ElementID: finding.ElementID, Source: "content", Path: &path}, Location: finding.Location}, Kind: finding.Kind})
		}
	}
	for _, ref := range incoming.References {
		var ids []string
		if ref.InboundID != "" {
			ids = []string{ref.InboundID}
		}
		add(CatalogFinding{ElementID: ref.RuleID, Location: ref.Location, Kind: "inbound", Path: ref.Path, PartIndex: ref.PartIndex, Status: ref.Status, ProviderIDs: ids}, ref.Status != "resolved")
	}
	usableBalancer := make(map[string]bool)
	for _, ref := range outgoing.References {
		if (ref.Kind == "selector" || ref.Kind == "fallback") && ref.Status == "resolved" {
			usableBalancer[ref.RuleID] = true
		}
	}
	for _, ref := range outgoing.References {
		add(CatalogFinding{ElementID: ref.ElementID, Location: ref.Location, Kind: ref.Kind, Path: ref.Path, PartIndex: ref.PartIndex, Status: ref.Status, ProviderIDs: ref.OutboundIDs}, ref.Status == "missing" || (ref.Status == "no_matches" && !usableBalancer[ref.RuleID]))
	}
	if len(blocking) > 0 {
		return nil, blocking
	}
	return findings, nil
}
