package publication

import (
	"slices"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type DraftInbound struct {
	ID                      string `json:"id"`
	Tag                     string `json:"tag"`
	Origin                  string `json:"origin"`
	Disabled                bool   `json:"disabled"`
	DependenciesUnavailable bool   `json:"dependenciesUnavailable,omitempty"`
}

type InboundDependency struct {
	RuleID string `json:"ruleId"`
	Label  string `json:"label"`
	Origin string `json:"origin"`
	Path   string `json:"path"`
}

type InboundInUse struct{ Dependencies []InboundDependency }

func (*InboundInUse) Error() string { return "inbound in use" }

type DraftInbounds struct {
	input configuration.InboundSnapshot
	draft routersettings.Draft
	items []DraftInbound
	rules map[string]inboundRuleSource
}

type inboundRuleSource struct {
	shared string
	local  []byte
}

func PrepareDraftInbounds(template *templates.Snapshot, draft routersettings.Draft, basis *OrderBasis) (*DraftInbounds, error) {
	if template == nil || template.Version < 1 || template.ID != draft.TemplateID {
		return nil, ErrSourceMismatch
	}
	input, issues := prepareCatalogInput(template, &draft)
	if len(issues) != 0 {
		return nil, &PreparationError{Issues: issues}
	}
	personalOrder, _, issues := preparePersonalOrder(input, template.Version, basis)
	if len(issues) != 0 {
		return nil, &PreparationError{Issues: issues}
	}
	prepared := &DraftInbounds{
		input: configuration.InboundSnapshot{
			Rules: *input.Rules, Inbounds: *input.Inbounds,
			TemplateOrder: input.TemplateOrder, PersonalOrder: personalOrder,
			DisabledRules: input.DisabledRules, DisabledInbounds: input.DisabledInbounds,
		},
		draft: draft, items: []DraftInbound{}, rules: make(map[string]inboundRuleSource, len(input.Rules.Shared)+len(input.Rules.Local)),
	}
	for _, item := range input.Rules.Shared {
		prepared.rules[item.Element.ID] = inboundRuleSource{shared: item.Element.Content}
	}
	for _, item := range input.Rules.Local {
		prepared.rules[item.ID] = inboundRuleSource{local: item.Content}
	}
	prepared.input.OtherShared, prepared.input.OtherLocal = otherDraftRefs(input, input.Outbounds, input.Routing)
	excluded := make(map[string]bool)
	for _, item := range input.Inbounds.Exclusions {
		excluded[item.Source.ElementID] = true
	}
	disabled := make(map[string]bool)
	for _, item := range input.DisabledInbounds {
		disabled[item.ID] = true
	}
	for _, item := range input.Inbounds.Shared {
		id := item.Element.ID
		if !excluded[id] {
			prepared.items = append(prepared.items, DraftInbound{ID: id, Origin: "shared", Disabled: disabled[id]})
		}
	}
	for _, item := range input.Inbounds.Local {
		prepared.items = append(prepared.items, DraftInbound{ID: item.ID, Origin: "local", Disabled: disabled[item.ID]})
	}
	return prepared, nil
}

func (p DraftInbounds) Inspect(selected string) ([]DraftInbound, string, []InboundDependency, error) {
	if selected == "" && len(p.items) != 0 {
		index := slices.IndexFunc(p.items, func(item DraftInbound) bool { return !item.Disabled })
		if index < 0 {
			index = 0
		}
		selected = p.items[index].ID
	}
	index := p.index(selected)
	if selected != "" && index < 0 {
		return nil, "", nil, ErrInvalidCandidate
	}
	analysis, issues := configuration.AnalyzeInboundDependencies(p.input)
	if len(issues) != 0 {
		return nil, "", nil, &PreparationError{Issues: ruleSelectionIssues(issues)}
	}
	items := slices.Clone(p.items)
	for i, entry := range items {
		items[i].Tag = analysis.Tags[entry.ID]
	}
	dependencies := []InboundDependency{}
	for _, reference := range analysis.References {
		if reference.InboundID == selected && selected != "" {
			dependencies = append(dependencies, p.dependency(reference.RuleID, reference.Path))
		}
	}
	if index >= 0 && items[index].Disabled {
		_, issues = configuration.GuardInboundChange(p.action(selected, configuration.InboundActionRemove))
		switch err := p.guardError(issues).(type) {
		case nil:
		case *InboundInUse:
			dependencies = err.Dependencies
		default:
			items[index].DependenciesUnavailable = true
		}
	}
	return items, selected, dependencies, nil
}

func (p DraftInbounds) Change(id string, action configuration.InboundAction) (routersettings.Draft, error) {
	index := p.index(id)
	if index < 0 {
		return routersettings.Draft{}, ErrInvalidCandidate
	}
	item := p.items[index]
	if item.Disabled && action == configuration.InboundActionDisable {
		return routersettings.Draft{}, ErrConflict
	}
	_, issues := configuration.GuardInboundChange(p.action(id, action))
	if err := p.guardError(issues); err != nil {
		return routersettings.Draft{}, err
	}

	next := p.draft
	next.DisabledInbounds = slices.Clone(next.DisabledInbounds)
	if action == configuration.InboundActionDisable {
		next.DisabledInbounds = append(next.DisabledInbounds, id)
		return next, nil
	}
	next.DisabledInbounds = slices.DeleteFunc(next.DisabledInbounds, func(value string) bool { return value == id })
	next.Groups = slices.Clone(next.Groups)
	group := slices.IndexFunc(next.Groups, func(group routersettings.Group) bool { return group.Name == "inbounds" })
	if group < 0 {
		group = len(next.Groups)
		next.Groups = append(next.Groups, routersettings.Group{Name: "inbounds"})
	}
	if item.Origin == "local" {
		next.Groups[group].Local = slices.DeleteFunc(slices.Clone(next.Groups[group].Local),
			func(element routersettings.LocalElement) bool { return element.ID == id })
	} else {
		next.Groups[group].Exclusions = append(slices.Clone(next.Groups[group].Exclusions),
			configuration.ElementSource{TemplateID: next.TemplateID, ElementID: id})
	}
	return next, nil
}

func (p DraftInbounds) index(id string) int {
	return slices.IndexFunc(p.items, func(item DraftInbound) bool { return item.ID == id })
}

func (p DraftInbounds) action(id string, action configuration.InboundAction) configuration.InboundActionInput {
	return configuration.InboundActionInput{
		Snapshot: p.input, Target: configuration.ElementRef{ID: id, Position: "/inbounds"}, Action: action,
	}
}

func (p DraftInbounds) guardError(issues []configuration.SelectionIssue) error {
	if len(issues) == 0 {
		return nil
	}
	var dependencies []InboundDependency
	for _, issue := range issues {
		if issue.Code != "inbound_in_use" || issue.Path == nil {
			return &PreparationError{Issues: ruleSelectionIssues(issues)}
		}
		dependencies = append(dependencies, p.dependency(issue.ElementID, *issue.Path))
	}
	return &InboundInUse{Dependencies: dependencies}
}

func (p DraftInbounds) dependency(id, path string) InboundDependency {
	source := p.rules[id]
	if source.local != nil {
		return InboundDependency{RuleID: id, Label: ruleLabel(id, source.local), Origin: "local", Path: path}
	}
	return InboundDependency{RuleID: id, Label: ruleLabel(id, []byte(source.shared)), Origin: "shared", Path: path}
}
