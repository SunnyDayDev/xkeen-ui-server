package publication

type ProfileOrder struct {
	State   string `json:"state"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

func summarizeProfileSwitchOrder(previous *VersionedRouterDraft, preview *ProfileSwitchPreview) ProfileOrder {
	if preview.PersonalOrder == nil {
		return ProfileOrder{State: "inherit"}
	}
	if previous == nil || previous.OrderBasis == nil || preview.OrderBasis == nil {
		return ProfileOrder{State: "retained"}
	}
	out := ProfileOrder{State: "reconciled"}
	old, next := make(map[string]bool), make(map[string]bool)
	for _, id := range previous.OrderBasis.SharedRuleIDs {
		old[id] = true
	}
	for _, id := range preview.OrderBasis.SharedRuleIDs {
		next[id] = true
		if !old[id] {
			out.Added++
		}
	}
	for id := range old {
		if !next[id] {
			out.Removed++
		}
	}
	return out
}
