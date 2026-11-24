package publication

import (
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type RouterPreviewMode string

const (
	PreviewDesired  RouterPreviewMode = "desired"
	PreviewPersonal RouterPreviewMode = "personal"
)

type RouterCatalogPreview struct {
	Mode     RouterPreviewMode
	Expected Expectations
	Files    []configuration.CatalogFile
}

// PreviewRouterFiles uses the same accepted input and assembler as publication.
// No validator or writer is involved. Desired mode copies the persisted bytes.
func PreviewRouterFiles(inputs *Inputs, mode RouterPreviewMode) (*RouterCatalogPreview, error) {
	if inputs == nil || inputs.Active == nil || inputs.Accepted == nil || inputs.Desired == nil {
		return nil, ErrSourceMissing
	}
	expected := Expectations{
		RouterID: inputs.RouterID, TemplateID: inputs.TemplateID, AcceptedVersion: inputs.Accepted.Version,
		ActiveGeneration: inputs.Active.Generation, DesiredNumber: inputs.Desired.Number,
	}
	if !validIdentity(expected.RouterID) || !validIdentity(expected.TemplateID) || expected.ActiveGeneration < 1 ||
		expected.AcceptedVersion < 1 || expected.DesiredNumber < 1 || !matchesExpectedInputs(inputs, expected) {
		return nil, ErrStoredState
	}
	switch mode {
	case PreviewDesired:
		return &RouterCatalogPreview{Mode: mode, Expected: expected, Files: copyCatalogFiles(inputs.Desired.Catalog.Files)}, nil
	case PreviewPersonal:
		if !inputs.RouterDraftRequested {
			return nil, ErrInvalidCommand
		}
	default:
		return nil, ErrInvalidCommand
	}
	draft, basis := inputs.Active.Draft, inputs.Active.OrderBasis
	var draftGeneration int64
	if saved := inputs.RouterDraft; saved != nil {
		if saved.Generation < 1 {
			return nil, ErrStoredState
		}
		draftGeneration, draft, basis = saved.Generation, saved.Draft, EditableRuleOrderBasis(saved, inputs.Active)
	}
	expected.RouterDraftGeneration = &draftGeneration
	if draft.ID != expected.RouterID || draft.TemplateID != expected.TemplateID {
		return nil, ErrSourceMismatch
	}
	if err := routersettings.ValidateDraft(draft); err != nil {
		return nil, ErrInvalidCandidate
	}
	prepared, issues := PrepareRouterCandidate(inputs.Accepted, &draft, basis)
	if len(issues) != 0 {
		return nil, &PreparationError{Issues: issues}
	}
	return &RouterCatalogPreview{Mode: mode, Expected: expected, Files: prepared.Catalog.Files}, nil
}
