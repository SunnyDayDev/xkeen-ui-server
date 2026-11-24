package publication

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/jsonvalue"
)

type DraftRuleEntry struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Origin   string `json:"origin"`
	Excluded bool   `json:"excluded"`
	Disabled bool   `json:"disabled"`
}

type DraftRules struct {
	Entries []DraftRuleEntry
	input   configuration.RuleOrderInput
	draft   routersettings.Draft
	basis   OrderBasis
	ids     map[string]bool
}

type LocalRuleSourceError struct{ Issue jsonvalue.Issue }

func (*LocalRuleSourceError) Error() string { return "invalid local rule JSON" }

// EditableRuleOrderBasis prefers the stored editable proof. A legacy draft may
// reuse the active proof only when it describes the same complete rule order.
func EditableRuleOrderBasis(saved *VersionedRouterDraft, active *ActiveSettings) *OrderBasis {
	if saved != nil && (saved.OrderBasis != nil || saved.Draft.PersonalOrder == nil) {
		return saved.OrderBasis
	}
	if active == nil {
		return nil
	}
	if saved == nil {
		return active.OrderBasis
	}
	if saved.Draft.ID != active.Draft.ID || saved.Draft.TemplateID != active.Draft.TemplateID ||
		active.Draft.PersonalOrder == nil || !slices.Equal(saved.Draft.PersonalOrder, active.Draft.PersonalOrder) ||
		!slices.Equal(localRuleIDs(saved.Draft), localRuleIDs(active.Draft)) {
		return nil
	}
	return active.OrderBasis
}

func localRuleIDs(draft routersettings.Draft) []string {
	for _, group := range draft.Groups {
		if group.Name == "rules" {
			ids := make([]string, len(group.Local))
			for i, item := range group.Local {
				ids[i] = item.ID
			}
			slices.Sort(ids)
			return ids
		}
	}
	return nil
}

// PrepareDraftRules builds the B08 order from identities without assembling Xray.
func PrepareDraftRules(template *templates.Snapshot, draft routersettings.Draft, basis *OrderBasis) (*DraftRules, error) {
	if template == nil || template.Version < 1 || template.ID != draft.TemplateID {
		return nil, ErrSourceMismatch
	}
	input := configuration.RuleOrderInput{
		Template:      configuration.Owner{Kind: "template", ID: template.ID},
		Router:        configuration.Owner{Kind: "router", ID: draft.ID},
		TemplateOrder: []string{},
		PersonalOrder: draft.PersonalOrder,
	}
	details := make(map[string]DraftRuleEntry)
	ids := make(map[string]bool)
	for _, group := range template.Sections {
		for i, item := range group.Elements {
			id := item.Element.ID
			ids[id] = true
			if group.Name == "rules" {
				input.Shared = append(input.Shared, configuration.ElementRef{ID: id, Position: "/rules/" + strconv.Itoa(i)})
				input.TemplateOrder = append(input.TemplateOrder, id)
				details[id] = DraftRuleEntry{ID: id, Label: ruleLabel(id, []byte(item.Element.Content)), Origin: "shared", Disabled: item.Disabled}
			}
		}
	}
	for _, group := range draft.Groups {
		for i, item := range group.Local {
			ids[item.ID] = true
			if group.Name == "rules" {
				input.Local = append(input.Local, configuration.ElementRef{ID: item.ID, Position: "/rules/local/" + strconv.Itoa(i)})
				details[item.ID] = DraftRuleEntry{ID: item.ID, Label: ruleLabel(item.ID, item.Content), Origin: "local"}
			}
		}
		if group.Name == "rules" {
			for i, source := range group.Exclusions {
				input.Exclusions = append(input.Exclusions, configuration.SharedExclusion{Source: source, Position: "/rules/exclusions/" + strconv.Itoa(i)})
			}
		}
	}
	personal, _, issues := alignRuleOrder(input, template.Version, basis)
	if len(issues) != 0 {
		return nil, &PreparationError{Issues: issues}
	}
	input.PersonalOrder = personal
	order, formIssues := configuration.AssembleRuleOrder(input)
	if len(formIssues) != 0 {
		return nil, &PreparationError{Issues: ruleSelectionIssues(formIssues)}
	}
	excluded := make(map[string]bool, len(input.Exclusions))
	for _, exclusion := range input.Exclusions {
		excluded[exclusion.Source.ElementID] = true
	}
	disabled := make(map[string]bool, len(draft.DisabledRules))
	for _, id := range draft.DisabledRules {
		disabled[id] = true
	}
	result := &DraftRules{Entries: make([]DraftRuleEntry, 0, len(order.Full)), input: input, draft: draft, ids: ids,
		basis: OrderBasis{TemplateID: template.ID, TemplateVersion: template.Version, SharedRuleIDs: slices.Clone(input.TemplateOrder)}}
	for _, rule := range order.Full {
		entry := details[rule.ID]
		entry.Excluded = excluded[rule.ID]
		entry.Disabled = entry.Disabled || disabled[rule.ID]
		result.Entries = append(result.Entries, entry)
	}
	return result, nil
}

func (r DraftRules) Insert(newID, afterID string, source json.RawMessage) (routersettings.Draft, *OrderBasis, error) {
	if !configuration.ValidElementID(newID) || r.ids[newID] {
		return routersettings.Draft{}, nil, ErrInvalidCandidate
	}
	index := 0
	if afterID != "" {
		position := slices.IndexFunc(r.Entries, func(rule DraftRuleEntry) bool { return rule.ID == afterID })
		if position < 0 {
			return routersettings.Draft{}, nil, ErrConflict
		}
		index = position + 1
	}
	if _, issue := jsonvalue.Parse(source); issue != nil {
		return routersettings.Draft{}, nil, &LocalRuleSourceError{Issue: *issue}
	}
	order, issues := configuration.InsertLocalRule(r.input, configuration.ElementRef{ID: newID, Position: "/rules/local"}, index)
	if len(issues) != 0 {
		return routersettings.Draft{}, nil, &PreparationError{Issues: ruleSelectionIssues(issues)}
	}
	next := r.draft
	next.PersonalOrder = order.PersonalOrder
	next.Groups = slices.Clone(next.Groups)
	item := routersettings.LocalElement{ID: newID, Content: bytes.Clone(source)}
	found := false
	for i, group := range next.Groups {
		if group.Name == "rules" {
			next.Groups[i].Local = append(slices.Clone(group.Local), item)
			found = true
			break
		}
	}
	if !found {
		next.Groups = append(next.Groups, routersettings.Group{Name: "rules", Local: []routersettings.LocalElement{item}})
	}
	basis := r.basis
	basis.SharedRuleIDs = slices.Clone(basis.SharedRuleIDs)
	return next, &basis, nil
}

// Reorder applies one move or a reset to the complete, reconciled rule list.
func (r DraftRules) Reorder(id string, position *int) (routersettings.Draft, *OrderBasis, error) {
	var order *configuration.RuleOrderCandidate
	var issues []configuration.SelectionIssue
	if position == nil {
		if id != "" {
			return routersettings.Draft{}, nil, ErrInvalidCandidate
		}
		order, issues = configuration.ResetRuleOrder(r.input)
	} else {
		order, issues = configuration.MoveRule(r.input, id, *position)
	}
	if len(issues) != 0 {
		return routersettings.Draft{}, nil, &PreparationError{Issues: ruleSelectionIssues(issues)}
	}
	next := r.draft
	next.PersonalOrder = order.PersonalOrder
	basis := r.basis
	basis.SharedRuleIDs = slices.Clone(basis.SharedRuleIDs)
	return next, &basis, nil
}

func ruleSelectionIssues(issues []configuration.SelectionIssue) []configuration.CatalogIssue {
	result := make([]configuration.CatalogIssue, len(issues))
	for i, issue := range issues {
		result[i] = configuration.CatalogIssue{SelectionIssue: issue}
	}
	return result
}

func ruleLabel(id string, source json.RawMessage) string {
	if tree, issue := jsonvalue.Parse(source); issue == nil {
		if object, ok := tree.(map[string]any); ok {
			if tag, ok := object["ruleTag"].(string); ok && tag != "" {
				return tag
			}
		}
	}
	return id
}
