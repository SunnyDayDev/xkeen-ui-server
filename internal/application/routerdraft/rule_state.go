package routerdraft

import (
	"slices"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

type RuleState struct {
	ElementID        string `json:"elementId"`
	Origin           string `json:"origin"`
	Position         int    `json:"position"`
	Disabled         bool   `json:"disabled"`
	PersonalDisabled bool   `json:"personalDisabled"`
	Excluded         bool   `json:"excluded"`
}

func ReadRuleState(s Snapshot, id string) (RuleState, error) {
	rules, err := PrepareRules(s)
	if err != nil {
		return RuleState{}, err
	}
	for i, entry := range rules.Entries {
		if entry.ID == id {
			return RuleState{id, entry.Origin, i, entry.Disabled, slices.Contains(s.Editable.DisabledRules, id), entry.Excluded}, nil
		}
	}
	return RuleState{}, ErrMissing
}

func EditRuleState(s Snapshot, id string, disabled bool) (routersettings.Draft, error) {
	state, err := ReadRuleState(s, id)
	if err != nil {
		return routersettings.Draft{}, err
	}
	draft := s.Editable
	if disabled != state.PersonalDisabled {
		draft.DisabledRules = slices.Clone(draft.DisabledRules)
		if disabled {
			draft.DisabledRules = append(draft.DisabledRules, id)
		} else {
			i := slices.Index(draft.DisabledRules, id)
			draft.DisabledRules = slices.Delete(draft.DisabledRules, i, i+1)
		}
	}
	if _, err := publication.AnalyzeDraftDependencies(s.Accepted, draft, s.OrderBasis); err != nil {
		return routersettings.Draft{}, err
	}
	return draft, nil
}
