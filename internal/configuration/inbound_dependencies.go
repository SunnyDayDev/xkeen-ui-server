package configuration

import (
	"strconv"
	"strings"
)

// InboundSnapshot is one router's supplied configuration state for inbound
// dependency analysis. Rules and inbounds retain their separate selection data.
type InboundSnapshot struct {
	Rules, Inbounds                 RouterSelectionInput
	TemplateOrder, PersonalOrder    []string
	DisabledRules, DisabledInbounds []ElementRef
	OtherShared, OtherLocal         []ElementRef
}

type InboundReference struct {
	RuleID    string
	InboundID string
	Location  ElementLocation
	Path      string
	PartIndex int
	Status    string
}

type parsedInboundReference struct {
	reference InboundReference
	tag       string
}

type InboundAnalysis struct {
	FullRules  []RuleOrderEntry
	References []InboundReference
	Ready      bool
}

type inboundAnalysisInternal struct {
	result         *InboundAnalysis
	activeInbounds map[string]EffectiveElement
}

type InboundAction string

const (
	InboundActionDisable InboundAction = "disable"
	InboundActionRemove  InboundAction = "remove"
)

type InboundActionInput struct {
	Snapshot InboundSnapshot
	Target   ElementRef
	Action   InboundAction
}

type InboundActionCandidate struct {
	TargetID string
	Action   InboundAction
	Ready    bool
}

// GuardInboundChange checks a target against the candidate's final rules.
func GuardInboundChange(input InboundActionInput) (*InboundActionCandidate, []SelectionIssue) {
	location := ElementLocation{Owner: input.Snapshot.Inbounds.Router, Position: input.Target.Position}
	if !validLocation(location) {
		return nil, invalidSelectionContext()
	}
	if !ValidElementID(input.Target.ID) {
		return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_element_id", Source: "record"}, Location: location}}
	}
	if input.Action != InboundActionDisable && input.Action != InboundActionRemove {
		return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_inbound_action", ElementID: input.Target.ID, Source: "record"}, Location: location}}
	}
	analysis, issues := analyzeInboundDependencies(input.Snapshot, input.Target.ID)
	if len(issues) != 0 {
		return nil, issues
	}
	target, found := analysis.activeInbounds[input.Target.ID]
	if !found {
		return nil, []SelectionIssue{{Issue: Issue{Code: "inbound_not_found", ElementID: input.Target.ID, Source: "record"}, Location: location}}
	}
	blockers := make([]SelectionIssue, 0)
	for _, ref := range analysis.result.References {
		if ref.InboundID != input.Target.ID {
			continue
		}
		path := ref.Path
		targetLocation := target.Location
		blockers = append(blockers, SelectionIssue{
			Issue:    Issue{Code: "inbound_in_use", ElementID: ref.RuleID, Source: "content", Path: &path},
			Location: ref.Location, OtherLocation: &targetLocation,
		})
	}
	if len(blockers) != 0 {
		return nil, blockers
	}
	return &InboundActionCandidate{TargetID: input.Target.ID, Action: input.Action, Ready: analysis.result.Ready}, nil
}

// AnalyzeInboundDependencies evaluates explicit inboundTag references only.
func AnalyzeInboundDependencies(input InboundSnapshot) (*InboundAnalysis, []SelectionIssue) {
	analysis, issues := analyzeInboundDependencies(input, "")
	if len(issues) != 0 {
		return nil, issues
	}
	return analysis.result, nil
}

// targetID is included as a pre-action provider while other candidate state
// is evaluated. Its disabled/excluded records are still metadata-validated.
func analyzeInboundDependencies(input InboundSnapshot, targetID string) (*inboundAnalysisInternal, []SelectionIssue) {
	if input.Rules.Template != input.Inbounds.Template || input.Rules.Router != input.Inbounds.Router {
		return nil, invalidSelectionContext()
	}
	if issues := validateSelectionMetadata(input.Rules); len(issues) != 0 {
		return nil, issues
	}
	if issues := validateSelectionMetadata(input.Inbounds); len(issues) != 0 {
		return nil, issues
	}
	shared, local := snapshotRefs(input)
	if issues := ValidateRouterLocalIDs(input.Rules.Template, shared, input.Rules.Router, local); len(issues) != 0 {
		return nil, selectionElementIssues(issues)
	}
	orderInput := orderInputFromSelection(OrderedRouterInput{Selection: input.Rules, TemplateOrder: input.TemplateOrder, PersonalOrder: input.PersonalOrder})
	order, issues := AssembleRuleOrder(orderInput)
	if len(issues) != 0 {
		return nil, issues
	}
	disabled, issues := validateDisabledRefs(input.Rules.Router, input.DisabledRules, order.Full, "duplicate_disabled_rule")
	if len(issues) != 0 {
		return nil, issues
	}
	allInbounds := make([]RuleOrderEntry, 0, len(input.Inbounds.Shared)+len(input.Inbounds.Local))
	for _, record := range input.Inbounds.Shared {
		allInbounds = append(allInbounds, RuleOrderEntry{ID: record.Element.ID})
	}
	for _, record := range input.Inbounds.Local {
		allInbounds = append(allInbounds, RuleOrderEntry{ID: record.ID})
	}
	disabledInbounds, issues := validateDisabledRefs(input.Inbounds.Router, input.DisabledInbounds, allInbounds, "duplicate_disabled_inbound")
	if len(issues) != 0 {
		return nil, issues
	}
	delete(disabledInbounds, targetID)
	active := make([]OrderedRule, 0, len(order.Visible))
	for _, entry := range order.Visible {
		if _, found := disabled[entry.ID]; found {
			continue
		}
		effective, issues := selectSnapshotElement(input.Rules, entry.ID)
		if len(issues) != 0 {
			return nil, issues
		}
		active = append(active, OrderedRule{ID: entry.ID, Effective: effective})
	}
	activeInbounds := make(map[string]EffectiveElement)
	excludedInbounds := make(map[string]struct{}, len(input.Inbounds.Exclusions))
	for _, exclusion := range input.Inbounds.Exclusions {
		excludedInbounds[exclusion.Source.ElementID] = struct{}{}
	}
	delete(excludedInbounds, targetID)
	for _, id := range allInboundIDs(input.Inbounds) {
		if _, found := disabledInbounds[id]; found {
			continue
		}
		if _, found := excludedInbounds[id]; found {
			continue
		}
		effective, issues := selectSnapshotElement(input.Inbounds, id)
		if len(issues) != 0 {
			return nil, issues
		}
		activeInbounds[id] = effective
	}
	parsed := make([]parsedInboundReference, 0)
	for _, rule := range active {
		found, issues := readInboundReferences(rule)
		if len(issues) != 0 {
			return nil, issues
		}
		parsed = append(parsed, found...)
	}
	providers, issues := indexActiveInbounds(input.Inbounds, activeInbounds, parsed)
	if len(issues) != 0 {
		return nil, issues
	}
	references := make([]InboundReference, len(parsed))
	ready := true
	for i, item := range parsed {
		references[i] = item.reference
		if provider, found := providers[item.tag]; found {
			references[i].InboundID = provider.id
			references[i].Status = "resolved"
		} else {
			references[i].Status = "missing"
			ready = false
		}
	}
	return &inboundAnalysisInternal{result: &InboundAnalysis{FullRules: order.Full, References: references, Ready: ready}, activeInbounds: activeInbounds}, nil
}

type inboundProvider struct {
	id       string
	location ElementLocation
}

func indexActiveInbounds(input RouterSelectionInput, active map[string]EffectiveElement, refs []parsedInboundReference) (map[string]inboundProvider, []SelectionIssue) {
	providers := make(map[string]inboundProvider)
	for _, id := range allInboundIDs(input) {
		element, found := active[id]
		if !found {
			continue
		}
		value, issue := parseJSON(element.Content, Issue{ElementID: id, Source: "content"})
		if issue != nil {
			return nil, []SelectionIssue{{Issue: *issue, Location: element.Location}}
		}
		obj, ok := value.(map[string]any)
		if !ok {
			path := ""
			return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_inbound_shape", ElementID: id, Source: "content", Path: &path}, Location: element.Location}}
		}
		raw, present := obj["tag"]
		if !present {
			continue
		}
		tag, ok := raw.(string)
		if !ok {
			path := "/tag"
			return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_inbound_tag", ElementID: id, Source: "content", Path: &path}, Location: element.Location}}
		}
		if previous, found := providers[tag]; found {
			path := "/tag"
			issues := []SelectionIssue{{Issue: Issue{Code: "ambiguous_inbound_tag", ElementID: id, Source: "content", Path: &path}, Location: element.Location, OtherLocation: &previous.location}}
			for _, ref := range refs {
				if ref.tag == tag {
					refPath := ref.reference.Path
					issues = append(issues, SelectionIssue{Issue: Issue{Code: "ambiguous_inbound_reference", ElementID: ref.reference.RuleID, Source: "content", Path: &refPath}, Location: ref.reference.Location})
				}
			}
			return nil, issues
		}
		providers[tag] = inboundProvider{id: id, location: element.Location}
	}
	return providers, nil
}

func readInboundReferences(rule OrderedRule) ([]parsedInboundReference, []SelectionIssue) {
	value, issue := parseJSON(rule.Effective.Content, Issue{ElementID: rule.ID, Source: "content"})
	if issue != nil {
		return nil, []SelectionIssue{{Issue: *issue, Location: rule.Effective.Location}}
	}
	obj, ok := value.(map[string]any)
	if !ok {
		path := ""
		return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_rule_shape", ElementID: rule.ID, Source: "content", Path: &path}, Location: rule.Effective.Location}}
	}
	raw, present := obj["inboundTag"]
	if !present {
		return nil, nil
	}
	refs := make([]parsedInboundReference, 0)
	newRef := func(path string, part int, tag string) parsedInboundReference {
		return parsedInboundReference{reference: InboundReference{RuleID: rule.ID, Location: rule.Effective.Location, Path: path, PartIndex: part}, tag: tag}
	}
	switch tags := raw.(type) {
	case string:
		for i, tag := range strings.Split(tags, ",") {
			refs = append(refs, newRef("/inboundTag", i, tag))
		}
	case []any:
		for i, value := range tags {
			tag, ok := value.(string)
			if !ok {
				path := "/inboundTag/" + strconv.Itoa(i)
				return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_inbound_tag", ElementID: rule.ID, Source: "content", Path: &path}, Location: rule.Effective.Location}}
			}
			refs = append(refs, newRef("/inboundTag/"+strconv.Itoa(i), i, tag))
		}
	default:
		path := "/inboundTag"
		return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_inbound_tag", ElementID: rule.ID, Source: "content", Path: &path}, Location: rule.Effective.Location}}
	}
	return refs, nil
}

func allInboundIDs(input RouterSelectionInput) []string {
	ids := make([]string, 0, len(input.Shared)+len(input.Local))
	for _, record := range input.Shared {
		ids = append(ids, record.Element.ID)
	}
	for _, record := range input.Local {
		ids = append(ids, record.ID)
	}
	return ids
}

func snapshotRefs(input InboundSnapshot) ([]ElementRef, []ElementRef) {
	shared := make([]ElementRef, 0, len(input.Rules.Shared)+len(input.Inbounds.Shared)+len(input.OtherShared))
	local := make([]ElementRef, 0, len(input.Rules.Local)+len(input.Inbounds.Local)+len(input.OtherLocal))
	for _, section := range []RouterSelectionInput{input.Rules, input.Inbounds} {
		for _, record := range section.Shared {
			shared = append(shared, ElementRef{ID: record.Element.ID, Position: record.Position})
		}
		for _, record := range section.Local {
			local = append(local, ElementRef{ID: record.ID, Position: record.Position})
		}
	}
	shared = append(shared, input.OtherShared...)
	local = append(local, input.OtherLocal...)
	return shared, local
}

func validateDisabledRefs(owner Owner, refs []ElementRef, order []RuleOrderEntry, duplicateCode string) (map[string]struct{}, []SelectionIssue) {
	known := make(map[string]struct{}, len(order))
	for _, entry := range order {
		known[entry.ID] = struct{}{}
	}
	disabled := make(map[string]struct{}, len(refs))
	first := make(map[string]ElementLocation, len(refs))
	for _, ref := range refs {
		location := ElementLocation{Owner: owner, Position: ref.Position}
		if !validLocation(location) {
			return nil, invalidSelectionContext()
		}
		if !ValidElementID(ref.ID) {
			return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_element_id", Source: "record"}, Location: location}}
		}
		if prev, found := first[ref.ID]; found {
			return nil, []SelectionIssue{{Issue: Issue{Code: duplicateCode, Source: "record", ElementID: ref.ID}, Location: location, OtherLocation: &prev}}
		}
		first[ref.ID] = location
		if _, found := known[ref.ID]; !found {
			return nil, []SelectionIssue{{Issue: Issue{Code: "disabled_element_not_found", Source: "record", ElementID: ref.ID}, Location: location}}
		}
		disabled[ref.ID] = struct{}{}
	}
	return disabled, nil
}

func selectSnapshotElement(input RouterSelectionInput, id string) (EffectiveElement, []SelectionIssue) {
	for i, shared := range input.Shared {
		if shared.Element.ID != id {
			continue
		}
		overrides := make(map[string]LocatedContentOverride, len(input.Overrides))
		for _, candidate := range input.Overrides {
			overrides[candidate.Override.Source.ElementID] = candidate
		}
		return selectSharedElement(input, shared, i, overrides)
	}
	for _, local := range input.Local {
		if local.ID != id {
			continue
		}
		return selectLocalElement(input.Router, local)
	}
	return EffectiveElement{}, []SelectionIssue{{Issue: Issue{Code: "order_element_not_found", ElementID: id, Source: "record"}, Location: ElementLocation{Owner: input.Router, Position: "/order"}}}
}
