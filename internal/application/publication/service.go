package publication

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

var (
	ErrInvalidCommand     = errors.New("invalid publication command")
	ErrSourceMissing      = errors.New("publication source is missing")
	ErrSourceMismatch     = errors.New("personal source differs from the publication scope")
	ErrInvalidCandidate   = errors.New("invalid personal publication candidate")
	ErrUnsupportedProfile = errors.New("incompatible revision profile")
	ErrPublicationRead    = errors.New("could not read publication sources")
	ErrPublicationCommit  = errors.New("could not commit publication")
)

type PublishMode string

const (
	PublishOrdinary PublishMode = "publish"
	ConfirmAccepted PublishMode = "confirm_accepted"
	ConfirmInitial  PublishMode = "confirm_initial"
)

type PersonalSource string

const (
	SourceActive     PersonalSource = "active"
	SourceSavedDraft PersonalSource = "saved_draft"
	SourceCandidate  PersonalSource = "candidate"
)

type PublishRequest struct {
	Mode       PublishMode
	Source     PersonalSource
	Expected   Expectations
	Candidate  *routersettings.Draft
	OrderBasis *OrderBasis
}

type PublishResult struct {
	Revision int64
	Changed  bool
}

// PreparationError contains only safe catalog diagnostics.
type PreparationError struct{ Issues []configuration.CatalogIssue }

func (e *PreparationError) Error() string { return "could not prepare publication catalog" }

type Service struct {
	store     Store
	validator CatalogValidator
}

func NewService(store Store, validator CatalogValidator) *Service {
	return &Service{store: store, validator: validator}
}

// Publish prepares and validates a single router snapshot before atomically
// committing its accepted settings and desired revision.
func (s *Service) Publish(ctx context.Context, request PublishRequest) (*PublishResult, error) {
	if !validPublishRequest(request) {
		return nil, ErrInvalidCommand
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request = ownPublishRequest(request)
	read := ReadRequest{
		RouterID: request.Expected.RouterID, IncludeRouterDraft: request.Source == SourceSavedDraft,
		IncludeTemplateDraft: request.Mode == ConfirmInitial,
	}
	if request.Mode != PublishOrdinary {
		read.InitialTemplateID = request.Expected.TemplateID
	}
	inputs, err := s.store.Read(ctx, read)
	if err != nil {
		return nil, safeStoreError(err, ErrPublicationRead)
	}
	if inputs == nil {
		return nil, ErrPublicationRead
	}
	if !matchesExpectedInputs(inputs, request.Expected) {
		return nil, ErrConflict
	}
	if inputs.Desired != nil && inputs.Desired.Catalog.Profile != configuration.XrayIdentityVersion {
		return nil, ErrUnsupportedProfile
	}

	shared := inputs.Accepted
	if request.Mode == ConfirmInitial {
		shared = inputs.TemplateDraft
	}
	settings, basis, err := selectPersonalSource(request, inputs)
	if err != nil {
		return nil, err
	}
	if err := routersettings.ValidateDraft(settings); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidCandidate, err)
	}
	prepared, issues := PrepareRouterCandidate(shared, &settings, basis)
	if len(issues) != 0 {
		return nil, &PreparationError{Issues: issues}
	}
	files := prepared.Catalog.Files
	if err := validateCatalogFiles(ctx, s.validator, files); err != nil {
		return nil, err
	}

	settings.PersonalOrder = prepared.PersonalOrder
	plan := CommitPlan{
		FirstConfirmation:     request.Mode != PublishOrdinary,
		AcceptInitialTemplate: request.Mode == ConfirmInitial,
		Expected:              request.Expected,
		Settings:              settings,
		OrderBasis:            prepared.OrderBasis,
	}
	if inputs.Desired == nil || !sameCatalogFiles(inputs.Desired.Catalog.Files, files) {
		plan.NewRevision = &VerifiedFiles{Profile: configuration.XrayIdentityVersion, Files: files}
	}
	number, err := s.store.Commit(ctx, plan)
	if err != nil {
		return nil, safeStoreError(err, ErrPublicationCommit)
	}
	return &PublishResult{Revision: number, Changed: plan.NewRevision != nil}, nil
}

func validPublishRequest(request PublishRequest) bool {
	expected := request.Expected
	if !validIdentity(expected.RouterID) || !validIdentity(expected.TemplateID) {
		return false
	}
	switch request.Source {
	case SourceActive:
		if request.Mode != PublishOrdinary || request.Candidate != nil || request.OrderBasis != nil || expected.RouterDraftGeneration != nil {
			return false
		}
	case SourceSavedDraft:
		if request.Candidate != nil || expected.RouterDraftGeneration == nil || *expected.RouterDraftGeneration < 0 {
			return false
		}
	case SourceCandidate:
		if request.Candidate == nil || expected.RouterDraftGeneration != nil {
			return false
		}
	default:
		return false
	}

	switch request.Mode {
	case PublishOrdinary:
		return expected.AcceptedVersion > 0 && expected.ActiveGeneration > 0 &&
			expected.DesiredNumber > 0 && expected.TemplateDraftVersion == nil
	case ConfirmAccepted:
		return expected.AcceptedVersion > 0 && expected.ActiveGeneration == 0 &&
			expected.DesiredNumber == 0 && expected.TemplateDraftVersion == nil
	case ConfirmInitial:
		return expected.AcceptedVersion == 0 && expected.ActiveGeneration == 0 &&
			expected.DesiredNumber == 0 && expected.TemplateDraftVersion != nil && *expected.TemplateDraftVersion > 0
	default:
		return false
	}
}

func matchesExpectedInputs(inputs *Inputs, expected Expectations) bool {
	if inputs.RouterID != expected.RouterID || inputs.TemplateID != expected.TemplateID {
		return false
	}
	var acceptedVersion, activeGeneration, desiredNumber int64
	if inputs.Accepted != nil {
		if inputs.Accepted.ID != expected.TemplateID {
			return false
		}
		acceptedVersion = inputs.Accepted.Version
	}
	if inputs.Active != nil {
		if inputs.Active.Draft.ID != expected.RouterID || inputs.Active.Draft.TemplateID != expected.TemplateID {
			return false
		}
		activeGeneration = inputs.Active.Generation
	}
	if inputs.Desired != nil {
		desiredNumber = inputs.Desired.Number
	}
	if acceptedVersion != expected.AcceptedVersion || activeGeneration != expected.ActiveGeneration || desiredNumber != expected.DesiredNumber {
		return false
	}
	if expected.TemplateDraftVersion != nil && (!inputs.TemplateDraftRequested || inputs.TemplateDraft == nil ||
		inputs.TemplateDraft.ID != expected.TemplateID || inputs.TemplateDraft.Version != *expected.TemplateDraftVersion) {
		return false
	}
	if expected.RouterDraftGeneration != nil {
		var generation int64
		if inputs.RouterDraft != nil {
			generation = inputs.RouterDraft.Generation
		}
		return inputs.RouterDraftRequested && generation == *expected.RouterDraftGeneration
	}
	return true
}

func selectPersonalSource(request PublishRequest, inputs *Inputs) (routersettings.Draft, *OrderBasis, error) {
	var draft routersettings.Draft
	basis := request.OrderBasis
	switch request.Source {
	case SourceActive:
		draft, basis = inputs.Active.Draft, inputs.Active.OrderBasis
	case SourceSavedDraft:
		if inputs.RouterDraft == nil {
			return routersettings.Draft{}, nil, ErrSourceMissing
		}
		draft = inputs.RouterDraft.Draft
	case SourceCandidate:
		draft = *request.Candidate
	}
	if draft.ID != request.Expected.RouterID || draft.TemplateID != request.Expected.TemplateID {
		return routersettings.Draft{}, nil, ErrSourceMismatch
	}
	return draft, basis, nil
}

func validateCatalogFiles(ctx context.Context, validator CatalogValidator, files []configuration.CatalogFile) error {
	if validator == nil {
		return ErrValidatorUnavailable
	}
	verification := copyCatalogFiles(files)
	err := validator.Validate(ctx, configuration.XrayIdentityVersion, verification)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !sameCatalogFiles(files, verification) {
		return ErrValidatorUnavailable
	}
	if errors.Is(err, ErrCatalogRejected) {
		return ErrCatalogRejected
	}
	if err != nil {
		return ErrValidatorUnavailable
	}
	return nil
}

// sameCatalogFiles compares the complete name-to-bytes mapping, regardless of order.
func sameCatalogFiles(first, second []configuration.CatalogFile) bool {
	if len(first) != len(second) {
		return false
	}
	contents := make(map[string][]byte, len(first))
	for _, file := range first {
		if _, exists := contents[file.Name]; exists {
			return false
		}
		contents[file.Name] = file.Content
	}
	for _, file := range second {
		content, exists := contents[file.Name]
		if !exists || !bytes.Equal(content, file.Content) {
			return false
		}
		delete(contents, file.Name)
	}
	return true
}

func safeStoreError(err, fallback error) error {
	for _, known := range []error{ErrConflict, ErrStoredState, ErrInvalidState, ErrVersionExhausted, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			return known
		}
	}
	return fallback
}
