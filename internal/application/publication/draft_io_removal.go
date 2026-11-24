package publication

import (
	"errors"
	"slices"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type DraftIOBlocker struct {
	RuleID string `json:"ruleId"`
	Kind   string `json:"kind"`
	Path   string `json:"path"`
}

type DraftIORemoval struct {
	Origin, Effect string
	Blockers       []DraftIOBlocker
	Draft          routersettings.Draft
}

func PrepareDraftIORemoval(template *templates.Snapshot, draft routersettings.Draft, basis *OrderBasis, section, id string) (DraftIORemoval, error) {
	switch section {
	case "inbounds":
		return prepareDraftInboundRemoval(template, draft, basis, id)
	case "outbounds":
		return prepareDraftOutboundRemoval(template, draft, basis, id)
	default:
		return DraftIORemoval{}, ErrInvalidCandidate
	}
}

func prepareDraftInboundRemoval(template *templates.Snapshot, draft routersettings.Draft, basis *OrderBasis, id string) (DraftIORemoval, error) {
	inbounds, err := PrepareDraftInbounds(template, draft, basis)
	if err != nil {
		return DraftIORemoval{}, err
	}
	index := inbounds.index(id)
	if index < 0 {
		return DraftIORemoval{}, ErrSourceMissing
	}
	result := draftIORemoval(inbounds.items[index].Origin)
	result.Draft, err = inbounds.Change(id, configuration.InboundActionRemove)
	var used *InboundInUse
	if errors.As(err, &used) {
		for _, dependency := range used.Dependencies {
			result.Blockers = append(result.Blockers, DraftIOBlocker{dependency.RuleID, "inbound", dependency.Path})
		}
		return result, nil
	}
	return result, err
}

func prepareDraftOutboundRemoval(template *templates.Snapshot, draft routersettings.Draft, basis *OrderBasis, id string) (DraftIORemoval, error) {
	_, outbounds, err := SelectDraftIO(template, draft)
	if err != nil {
		return DraftIORemoval{}, err
	}
	index := slices.IndexFunc(outbounds, func(member configuration.CatalogIOMember) bool { return member.ID == id })
	if index < 0 {
		return DraftIORemoval{}, ErrSourceMissing
	}
	result := draftIORemoval(outbounds[index].Origin)
	before, err := AnalyzeDraftDependencies(template, draft, basis)
	if err != nil {
		return DraftIORemoval{}, err
	}
	next := removeDraftIOElement(draft, "outbounds", id, result.Origin)
	after, err := AnalyzeDraftDependencies(template, next, basis)
	if err != nil {
		return DraftIORemoval{}, err
	}
	result.Blockers = outboundRemovalBlockers(id, before.Outbound.References, after.Outbound.References)
	if len(result.Blockers) == 0 {
		result.Draft = next
	}
	return result, nil
}

func outboundRemovalBlockers(id string, before, after []configuration.OutboundReference) []DraftIOBlocker {
	usable := make(map[string]bool)
	for _, ref := range after {
		if (ref.Kind == "selector" || ref.Kind == "fallback") && ref.Status == "resolved" {
			usable[ref.RuleID] = true
		}
	}
	blockers := []DraftIOBlocker{}
	for _, ref := range before {
		if !slices.Contains(ref.OutboundIDs, id) {
			continue
		}
		switch ref.Kind {
		case "direct", "fallback":
			blockers = append(blockers, DraftIOBlocker{ref.RuleID, ref.Kind, ref.Path})
		case "selector":
			if !usable[ref.RuleID] {
				blockers = append(blockers, DraftIOBlocker{ref.RuleID, ref.Kind, ref.Path})
			}
		}
	}
	return blockers
}

func draftIORemoval(origin string) DraftIORemoval {
	effect := "excluded"
	if origin == "local" {
		effect = "removed"
	}
	return DraftIORemoval{Origin: origin, Effect: effect, Blockers: []DraftIOBlocker{}}
}

func removeDraftIOElement(draft routersettings.Draft, section, id, origin string) routersettings.Draft {
	draft.Groups = slices.Clone(draft.Groups)
	index := slices.IndexFunc(draft.Groups, func(group routersettings.Group) bool { return group.Name == section })
	if index < 0 {
		index = len(draft.Groups)
		draft.Groups = append(draft.Groups, routersettings.Group{Name: section})
	}
	group := &draft.Groups[index]
	if origin == "local" {
		group.Local = slices.DeleteFunc(slices.Clone(group.Local), func(element routersettings.LocalElement) bool { return element.ID == id })
	} else {
		group.Exclusions = append(slices.Clone(group.Exclusions), configuration.ElementSource{TemplateID: draft.TemplateID, ElementID: id})
	}
	return draft
}
