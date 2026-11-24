package routerdraft

import "github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"

func PrepareRules(snapshot Snapshot) (*publication.DraftRules, error) {
	if snapshot.Editable.TemplateID != snapshot.Basis.TemplateID {
		return nil, ErrForeign
	}
	return publication.PrepareDraftRules(snapshot.Accepted, snapshot.Editable, snapshot.OrderBasis)
}
