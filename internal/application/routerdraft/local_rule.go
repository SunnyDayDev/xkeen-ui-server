package routerdraft

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/jsonvalue"
)

// localRulePosition limits inspection and replacement to the local rules of this
// router using the same complete order basis as the rule editor.
func localRulePosition(snapshot Snapshot, id string) (int, int, error) {
	if _, err := PrepareRules(snapshot); err != nil {
		return 0, 0, err
	}
	for i, g := range snapshot.Editable.Groups {
		if g.Name != "rules" {
			continue
		}
		for j, item := range g.Local {
			if item.ID == id {
				return i, j, nil
			}
		}
	}
	return 0, 0, ErrMissing
}

func ReadLocalRule(snapshot Snapshot, id string) (string, error) {
	i, j, err := localRulePosition(snapshot, id)
	if err != nil {
		return "", err
	}
	return string(snapshot.Editable.Groups[i].Local[j].Content), nil
}

func EditLocalRule(snapshot Snapshot, id string, source json.RawMessage) (routersettings.Draft, error) {
	i, j, err := localRulePosition(snapshot, id)
	if err != nil {
		return routersettings.Draft{}, err
	}
	if _, issue := jsonvalue.Parse(source); issue != nil {
		return routersettings.Draft{}, &publication.LocalRuleSourceError{Issue: *issue}
	}
	draft := snapshot.Editable
	draft.Groups = slices.Clone(draft.Groups)
	group := &draft.Groups[i]
	group.Local = slices.Clone(group.Local)
	group.Local[j].Content = bytes.Clone(source)
	return draft, nil
}
