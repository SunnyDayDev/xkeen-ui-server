package publication

import (
	"slices"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type DraftInboundState struct {
	Origin           string           `json:"origin"`
	PersonalDisabled bool             `json:"personalDisabled"`
	TemplateDisabled bool             `json:"templateDisabled"`
	Excluded         bool             `json:"excluded"`
	Active           bool             `json:"active"`
	References       []DraftIOBlocker `json:"references"`
}

func (p DraftInbounds) InboundState(id string) (DraftInboundState, error) {
	state, err := p.inboundState(id)
	if err != nil {
		return DraftInboundState{}, err
	}
	analysis, issues := configuration.AnalyzeInboundDependencies(p.input)
	if len(issues) != 0 {
		return DraftInboundState{}, &PreparationError{Issues: ruleSelectionIssues(issues)}
	}
	for _, ref := range analysis.References {
		if ref.InboundID == id {
			state.References = append(state.References, DraftIOBlocker{ref.RuleID, "inbound", ref.Path})
		}
	}
	return state, nil
}

func (p DraftInbounds) SetInboundDisabled(id string, disabled bool) (routersettings.Draft, error) {
	state, err := p.inboundState(id)
	if err != nil {
		return routersettings.Draft{}, err
	}
	if state.PersonalDisabled == disabled {
		return routersettings.Draft{}, ErrConflict
	}
	if disabled {
		return p.Change(id, configuration.InboundActionDisable)
	}

	next := p.draft
	next.DisabledInbounds = slices.DeleteFunc(slices.Clone(next.DisabledInbounds), func(value string) bool { return value == id })
	// A shared disable survives removal of the local flag, and excluded
	// elements remain excluded. Analyze exactly the resulting selection.
	input := p.input
	input.DisabledInbounds = slices.DeleteFunc(slices.Clone(input.DisabledInbounds), func(ref configuration.ElementRef) bool { return ref.ID == id })
	if shared, found := p.templateDisabled[id]; found {
		input.DisabledInbounds = append(input.DisabledInbounds, shared)
	}
	_, issues := configuration.AnalyzeInboundDependencies(input)
	if len(issues) != 0 {
		return routersettings.Draft{}, &PreparationError{Issues: ruleSelectionIssues(issues)}
	}
	return next, nil
}

func (p DraftInbounds) inboundState(id string) (DraftInboundState, error) {
	state := DraftInboundState{PersonalDisabled: slices.Contains(p.draft.DisabledInbounds, id), References: []DraftIOBlocker{}}
	selection := p.input.Inbounds
	for _, row := range selection.Shared {
		if row.Element.ID != id {
			continue
		}
		state.Origin = "shared"
		_, state.TemplateDisabled = p.templateDisabled[id]
		state.Excluded = slices.ContainsFunc(selection.Exclusions, func(exclusion configuration.SharedExclusion) bool {
			return exclusion.Source.ElementID == id
		})
		break
	}
	if state.Origin == "" && slices.ContainsFunc(selection.Local, func(row configuration.LocalContent) bool { return row.ID == id }) {
		state.Origin = "local"
	}
	if state.Origin == "" {
		return DraftInboundState{}, ErrSourceMissing
	}
	state.Active = !state.PersonalDisabled && !state.TemplateDisabled && !state.Excluded
	return state, nil
}
