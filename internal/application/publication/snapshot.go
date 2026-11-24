package publication

import (
	"encoding/json"
	"slices"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func ownPublishRequest(request PublishRequest) PublishRequest {
	request.Expected.TemplateDraftVersion = copyVersion(request.Expected.TemplateDraftVersion)
	request.Expected.RouterDraftGeneration = copyVersion(request.Expected.RouterDraftGeneration)
	if request.Candidate != nil {
		candidate := copyPersonalDraft(*request.Candidate)
		request.Candidate = &candidate
	}
	if request.OrderBasis != nil {
		basis := *request.OrderBasis
		basis.SharedRuleIDs = slices.Clone(basis.SharedRuleIDs)
		request.OrderBasis = &basis
	}
	return request
}

func copyVersion(version *int64) *int64 {
	if version == nil {
		return nil
	}
	copied := *version
	return &copied
}

func copyPersonalDraft(draft routersettings.Draft) routersettings.Draft {
	draft.PersonalOrder = slices.Clone(draft.PersonalOrder)
	draft.DisabledRules = slices.Clone(draft.DisabledRules)
	draft.DisabledInbounds = slices.Clone(draft.DisabledInbounds)
	draft.Groups = slices.Clone(draft.Groups)
	for i := range draft.Groups {
		group := &draft.Groups[i]
		group.Local = slices.Clone(group.Local)
		for j := range group.Local {
			group.Local[j].Content = slices.Clone(group.Local[j].Content)
		}
		group.Exclusions = slices.Clone(group.Exclusions)
		group.Overrides = slices.Clone(group.Overrides)
		for j := range group.Overrides {
			override := &group.Overrides[j]
			override.Content = slices.Clone(override.Content)
			if override.Values != nil {
				values := make(map[string]json.RawMessage, len(override.Values))
				for name, value := range override.Values {
					values[name] = slices.Clone(value)
				}
				override.Values = values
			}
		}
	}
	return draft
}

func copyCatalogFiles(files []configuration.CatalogFile) []configuration.CatalogFile {
	copied := slices.Clone(files)
	for i := range copied {
		copied[i].Content = slices.Clone(copied[i].Content)
	}
	return copied
}
