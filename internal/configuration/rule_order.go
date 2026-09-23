package configuration

import (
	"slices"
	"sort"
	"strconv"
)

// RuleOrderInput is the complete rule identity snapshot. A nil PersonalOrder
// inherits the template order; a non-nil empty slice is an explicit empty order.
type RuleOrderInput struct {
	Template, Router             Owner
	Shared, Local                []ElementRef
	TemplateOrder, PersonalOrder []string
	Exclusions                   []SharedExclusion
}

// RuleOrderEntry identifies a rule without interpreting its JSON.
type RuleOrderEntry struct {
	ID       string
	Origin   string
	Location ElementLocation
	Source   *ElementSource
}

// RuleOrder keeps excluded shared rules in Full and omits them from Visible.
type RuleOrder struct {
	Full, Visible []RuleOrderEntry
}

// RuleOrderCandidate is an independent edit result, not persisted state.
type RuleOrderCandidate struct {
	Full, PersonalOrder []string
}

// OrderedRouterInput combines B06 content selection with current rule order.
type OrderedRouterInput struct {
	Selection                    RouterSelectionInput
	TemplateOrder, PersonalOrder []string
}

// OrderedRule holds already-selected B06 content at its visible position.
type OrderedRule struct {
	ID        string
	Effective EffectiveElement
}

// OrderedRouterSelection contains full rule identities and visible content.
type OrderedRouterSelection struct {
	Full    []RuleOrderEntry
	Visible []OrderedRule
}

// SelectOrderedRouterElements selects content once via B06, then orders it.
// Errors return no partial result; no input is changed.
func SelectOrderedRouterElements(input OrderedRouterInput) (*OrderedRouterSelection, []SelectionIssue) {
	selected, issues := SelectRouterElements(input.Selection)
	if len(issues) != 0 {
		return nil, issues
	}
	order, issues := AssembleRuleOrder(orderInputFromSelection(input))
	if len(issues) != 0 {
		return nil, issues
	}
	result := &OrderedRouterSelection{Full: order.Full, Visible: make([]OrderedRule, 0, len(order.Visible))}
	for _, entry := range order.Visible {
		element, exists := selected.Elements[entry.ID]
		if !exists {
			return nil, []SelectionIssue{{Issue: Issue{Code: "order_element_not_found", ElementID: entry.ID, Source: "record"}, Location: entry.Location}}
		}
		result.Visible = append(result.Visible, OrderedRule{ID: entry.ID, Effective: element})
	}
	return result, nil
}

func orderInputFromSelection(input OrderedRouterInput) RuleOrderInput {
	orderInput := RuleOrderInput{
		Template: input.Selection.Template, Router: input.Selection.Router,
		TemplateOrder: input.TemplateOrder, PersonalOrder: input.PersonalOrder,
		Exclusions: input.Selection.Exclusions,
	}
	for _, shared := range input.Selection.Shared {
		orderInput.Shared = append(orderInput.Shared, ElementRef{ID: shared.Element.ID, Position: shared.Position})
	}
	for _, local := range input.Selection.Local {
		orderInput.Local = append(orderInput.Local, ElementRef{ID: local.ID, Position: local.Position})
	}
	return orderInput
}

// InsertLocalRule inserts a new local UUID into the current full order.
func InsertLocalRule(input RuleOrderInput, local ElementRef, index int) (*RuleOrderCandidate, []SelectionIssue) {
	order, issues := AssembleRuleOrder(input)
	if len(issues) != 0 {
		return nil, issues
	}
	if index < 0 || index > len(order.Full) {
		return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_order_index", Source: "record"}, Location: ElementLocation{Owner: input.Router, Position: "/order"}}}
	}
	location := ElementLocation{Owner: input.Router, Position: local.Position}
	if !validLocation(location) {
		return nil, invalidSelectionContext()
	}
	if !ValidElementID(local.ID) {
		return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_element_id", Source: "record"}, Location: location}}
	}
	for i, entry := range order.Full {
		if entry.ID == local.ID {
			first := ElementLocation{Owner: input.Router, Position: "/order/" + strconv.Itoa(i)}
			return nil, []SelectionIssue{{Issue: Issue{Code: "duplicate_order_element", ElementID: local.ID, Source: "record"}, Location: location, OtherLocation: &first}}
		}
	}
	ids := ruleOrderIDs(order.Full)
	ids = slices.Insert(ids, index, local.ID)
	return personalizedCandidate(ids), nil
}

// MoveRule uses an index in the list after removing the moved UUID.
func MoveRule(input RuleOrderInput, id string, index int) (*RuleOrderCandidate, []SelectionIssue) {
	order, issues := AssembleRuleOrder(input)
	if len(issues) != 0 {
		return nil, issues
	}
	location := ElementLocation{Owner: input.Router, Position: "/order"}
	if !ValidElementID(id) {
		return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_element_id", Source: "record"}, Location: location}}
	}
	if index < 0 || index >= len(order.Full) {
		return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_order_index", Source: "record"}, Location: location}}
	}
	ids := ruleOrderIDs(order.Full)
	old := slices.Index(ids, id)
	if old < 0 {
		return nil, []SelectionIssue{{Issue: Issue{Code: "order_element_not_found", ElementID: id, Source: "record"}, Location: location}}
	}
	ids = slices.Delete(ids, old, old+1)
	ids = slices.Insert(ids, index, id)
	return personalizedCandidate(ids), nil
}

// ReconcileRuleOrder applies old-to-new template changes to a router order.
func ReconcileRuleOrder(input RuleOrderInput, nextShared []ElementRef, nextTemplateOrder []string) (*RuleOrderCandidate, []SelectionIssue) {
	old, issues := AssembleRuleOrder(input)
	if len(issues) != 0 {
		return nil, issues
	}
	next := RuleOrderInput{
		Template: input.Template, Router: input.Router,
		Shared: nextShared, TemplateOrder: nextTemplateOrder,
	}
	if _, issues := AssembleRuleOrder(next); len(issues) != 0 {
		return nil, issues
	}
	if input.PersonalOrder == nil {
		full := make([]string, len(nextTemplateOrder))
		copy(full, nextTemplateOrder)
		return &RuleOrderCandidate{Full: full}, nil
	}
	oldShared := make(map[string]struct{}, len(input.Shared))
	for _, ref := range input.Shared {
		oldShared[ref.ID] = struct{}{}
	}
	newShared := make(map[string]struct{}, len(nextShared))
	for _, ref := range nextShared {
		newShared[ref.ID] = struct{}{}
	}
	ids := make([]string, 0, len(old.Full))
	for _, entry := range old.Full {
		if entry.Origin == "local" {
			ids = append(ids, entry.ID)
		} else if _, exists := newShared[entry.ID]; exists {
			ids = append(ids, entry.ID)
		}
	}
	for i, id := range nextTemplateOrder {
		if _, existed := oldShared[id]; !existed {
			ids = slices.Insert(ids, i, id)
		}
	}
	next.Local = input.Local
	next.PersonalOrder = ids
	if _, issues := AssembleRuleOrder(next); len(issues) != 0 {
		return nil, issues
	}
	return personalizedCandidate(ids), nil
}

// ResetRuleOrder restores shared order while retaining local numeric slots.
func ResetRuleOrder(input RuleOrderInput) (*RuleOrderCandidate, []SelectionIssue) {
	order, issues := AssembleRuleOrder(input)
	if len(issues) != 0 {
		return nil, issues
	}
	ids := make([]string, 0, len(order.Full))
	sharedIndex := 0
	localCount := 0
	for _, entry := range order.Full {
		if entry.Origin == "local" {
			ids = append(ids, entry.ID)
			localCount++
		} else {
			ids = append(ids, input.TemplateOrder[sharedIndex])
			sharedIndex++
		}
	}
	if localCount == 0 {
		return &RuleOrderCandidate{Full: ids}, nil
	}
	return personalizedCandidate(ids), nil
}

func personalizedCandidate(ids []string) *RuleOrderCandidate {
	return &RuleOrderCandidate{Full: slices.Clone(ids), PersonalOrder: slices.Clone(ids)}
}

func ruleOrderIDs(entries []RuleOrderEntry) []string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	return ids
}

func invalidSelectionContext() []SelectionIssue {
	return []SelectionIssue{{Issue: Issue{Code: "invalid_element_record", Source: "record"}}}
}

// AssembleRuleOrder validates the complete UUID lists before exclusions.
func AssembleRuleOrder(input RuleOrderInput) (*RuleOrder, []SelectionIssue) {
	metadata := RouterSelectionInput{Template: input.Template, Router: input.Router, Exclusions: input.Exclusions}
	for _, shared := range input.Shared {
		metadata.Shared = append(metadata.Shared, SharedElement{Element: Element{ID: shared.ID}, Position: shared.Position})
	}
	for _, local := range input.Local {
		metadata.Local = append(metadata.Local, LocalContent{ID: local.ID, Position: local.Position})
	}
	if issues := validateSelectionMetadata(metadata); len(issues) != 0 {
		return nil, issues
	}
	sharedEntries := make(map[string]RuleOrderEntry, len(input.Shared))
	entries := make(map[string]RuleOrderEntry, len(input.Shared)+len(input.Local))
	for _, shared := range input.Shared {
		entry := RuleOrderEntry{
			ID: shared.ID, Origin: "shared",
			Location: ElementLocation{Owner: input.Template, Position: shared.Position},
			Source:   &ElementSource{TemplateID: input.Template.ID, ElementID: shared.ID},
		}
		sharedEntries[shared.ID] = entry
		entries[shared.ID] = entry
	}
	for _, local := range input.Local {
		entries[local.ID] = RuleOrderEntry{
			ID: local.ID, Origin: "local",
			Location: ElementLocation{Owner: input.Router, Position: local.Position},
		}
	}
	if issues := validateOrderedIDs(input.TemplateOrder, sharedEntries, input.Template, "/templateOrder"); len(issues) != 0 {
		return nil, issues
	}
	ids := input.PersonalOrder
	if ids == nil {
		ids = input.TemplateOrder
	}
	if issues := validateOrderedIDs(ids, entries, input.Router, "/order"); len(issues) != 0 {
		return nil, issues
	}
	full := make([]RuleOrderEntry, 0, len(ids))
	excluded := make(map[string]struct{}, len(input.Exclusions))
	for _, exclusion := range input.Exclusions {
		excluded[exclusion.Source.ElementID] = struct{}{}
	}
	visible := make([]RuleOrderEntry, 0, len(ids))
	for _, id := range ids {
		entry := entries[id]
		full = append(full, entry)
		if _, hidden := excluded[id]; !hidden {
			visible = append(visible, cloneRuleOrderEntry(entry))
		}
	}
	return &RuleOrder{Full: full, Visible: visible}, nil
}

func cloneRuleOrderEntry(entry RuleOrderEntry) RuleOrderEntry {
	if entry.Source != nil {
		source := *entry.Source
		entry.Source = &source
	}
	return entry
}

func validateOrderedIDs(ids []string, expected map[string]RuleOrderEntry, owner Owner, position string) []SelectionIssue {
	seen := make(map[string]ElementLocation, len(ids))
	for i, id := range ids {
		location := ElementLocation{Owner: owner, Position: position + "/" + strconv.Itoa(i)}
		if !ValidElementID(id) {
			return []SelectionIssue{{Issue: Issue{Code: "invalid_element_id", Source: "record"}, Location: location}}
		}
		if first, exists := seen[id]; exists {
			return []SelectionIssue{{Issue: Issue{Code: "duplicate_order_element", ElementID: id, Source: "record"}, Location: location, OtherLocation: &first}}
		}
		seen[id] = location
		if _, exists := expected[id]; !exists {
			return []SelectionIssue{{Issue: Issue{Code: "order_element_not_found", ElementID: id, Source: "record"}, Location: location}}
		}
	}
	missing := make([]string, 0)
	for id := range expected {
		if _, found := seen[id]; !found {
			missing = append(missing, id)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		id := missing[0]
		return []SelectionIssue{{Issue: Issue{Code: "order_element_not_found", ElementID: id, Source: "record"}, Location: expected[id].Location}}
	}
	return nil
}
