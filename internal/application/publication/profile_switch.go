package publication

import (
	"context"
	"errors"
	"math"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

var ErrProfileAlreadyActive = errors.New("profile is already active")

// ProfileSwitchBasis scopes every mutable input used to switch one router.
type ProfileSwitchBasis struct {
	RouterID, SourceTemplateID, TargetTemplateID string
	SourceAcceptedVersion, TargetAcceptedVersion int64
	ActiveGeneration, DesiredNumber              int64
	SlotTemplateID                               string
	SlotGeneration, TargetDraftGeneration        int64
}

type ProfileSwitchInputs struct {
	Basis         ProfileSwitchBasis
	Current       *ActiveSettings
	Target        *templates.Snapshot
	TargetDraft   *VersionedRouterDraft
	Desired       *Revision
	Exclusions    *configuration.ExclusionSource
	AlreadyActive bool
}

type ProfileSwitchStore interface {
	ReadProfileSwitch(context.Context, string, string) (*ProfileSwitchInputs, error)
	CommitProfileSwitch(context.Context, ProfileSwitchPlan) (int64, error)
}

type ProfileSwitchPlan struct {
	Expected    ProfileSwitchBasis
	Settings    routersettings.Draft
	OrderBasis  *OrderBasis
	NewRevision *VerifiedFiles
}

type ProfileVariableEdit struct {
	ElementID, Name string
	Source          *string // Nil restores inheritance.
}

type ProfileSwitchRequest struct {
	RouterID, TargetTemplateID string
	Values                     []ProfileVariableEdit
}

type ProfileSwitchProof struct {
	Basis       ProfileSwitchBasis
	Fingerprint [32]byte
}

type ProfileSwitchPreview struct {
	Proof              ProfileSwitchProof
	Changed, Restoring bool
	PersonalOrder      []string
	OrderBasis         *OrderBasis
	Changes            []CatalogFileChange
}

type ProfileSwitchService struct {
	store     ProfileSwitchStore
	validator CatalogValidator
}

func NewProfileSwitchService(store ProfileSwitchStore, validator CatalogValidator) *ProfileSwitchService {
	return &ProfileSwitchService{store: store, validator: validator}
}

func (s *ProfileSwitchService) Read(ctx context.Context, routerID, targetTemplateID string) (*ProfileSwitchInputs, error) {
	if !validIdentity(routerID) || !validIdentity(targetTemplateID) {
		return nil, ErrInvalidCommand
	}
	inputs, err := s.store.ReadProfileSwitch(ctx, routerID, targetTemplateID)
	if err != nil {
		if errors.Is(err, ErrSourceMissing) {
			return nil, ErrSourceMissing
		}
		return nil, safeStoreError(err, ErrPublicationRead)
	}
	if !validProfileSwitchInputs(inputs, routerID, targetTemplateID) {
		return nil, ErrStoredState
	}
	return inputs, nil
}

func validProfileSwitchInputs(in *ProfileSwitchInputs, routerID, targetID string) bool {
	if in == nil || in.Current == nil || in.Target == nil || in.Desired == nil {
		return false
	}
	b := in.Basis
	if b.RouterID != routerID || in.Current.Draft.ID != routerID ||
		b.SourceTemplateID != in.Current.Draft.TemplateID || !validIdentity(b.SourceTemplateID) ||
		b.TargetTemplateID != targetID || in.Target.ID != targetID ||
		b.SourceAcceptedVersion < 1 || b.TargetAcceptedVersion != in.Target.Version || in.Target.Version < 1 ||
		b.ActiveGeneration != in.Current.Generation || b.ActiveGeneration < 1 ||
		b.DesiredNumber != in.Desired.Number || b.DesiredNumber < 1 ||
		b.SlotGeneration < 0 || b.TargetDraftGeneration < 0 ||
		(b.SlotGeneration > 0) != validIdentity(b.SlotTemplateID) ||
		in.AlreadyActive != (b.SourceTemplateID == targetID) {
		return false
	}
	if in.TargetDraft == nil {
		return b.TargetDraftGeneration == 0
	}
	return b.TargetDraftGeneration > 0 && b.TargetDraftGeneration == in.TargetDraft.Generation &&
		in.TargetDraft.Draft.ID == routerID && in.TargetDraft.Draft.TemplateID == targetID
}

func (s *ProfileSwitchService) Preview(ctx context.Context, request ProfileSwitchRequest) (*ProfileSwitchPreview, error) {
	inputs, err := s.Read(ctx, request.RouterID, request.TargetTemplateID)
	if err != nil {
		return nil, err
	}
	if inputs.AlreadyActive {
		return nil, ErrProfileAlreadyActive
	}
	plan, preview, files, err := prepareProfileSwitch(inputs, request.Values)
	if err == nil {
		err = validateCatalogFiles(ctx, s.validator, files)
	}
	if freshness := s.checkProfileSwitch(ctx, inputs.Basis); freshness != nil {
		return nil, freshness
	}
	if err != nil {
		return nil, err
	}
	preview.Changed = plan.NewRevision != nil
	return preview, nil
}

func (s *ProfileSwitchService) Confirm(ctx context.Context, request ProfileSwitchRequest, proof ProfileSwitchProof) (*PublishResult, error) {
	if request.RouterID != proof.Basis.RouterID || request.TargetTemplateID != proof.Basis.TargetTemplateID {
		return nil, ErrConflict
	}
	inputs, err := s.Read(ctx, request.RouterID, request.TargetTemplateID)
	if errors.Is(err, ErrSourceMissing) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	if inputs.AlreadyActive || inputs.Basis != proof.Basis {
		return nil, ErrConflict
	}
	plan, current, files, err := prepareProfileSwitch(inputs, request.Values)
	if err == nil && current.Proof != proof {
		return nil, ErrConflict
	}
	if err == nil {
		err = validateCatalogFiles(ctx, s.validator, files)
	}
	if err != nil {
		if freshness := s.checkProfileSwitch(ctx, inputs.Basis); freshness != nil {
			return nil, freshness
		}
		return nil, err
	}
	number, err := s.store.CommitProfileSwitch(ctx, plan)
	if err != nil {
		return nil, safeStoreError(err, ErrPublicationCommit)
	}
	return &PublishResult{Revision: number, Changed: plan.NewRevision != nil}, nil
}

func (s *ProfileSwitchService) checkProfileSwitch(ctx context.Context, basis ProfileSwitchBasis) error {
	current, err := s.Read(ctx, basis.RouterID, basis.TargetTemplateID)
	if errors.Is(err, ErrSourceMissing) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if current.AlreadyActive || current.Basis != basis {
		return ErrConflict
	}
	return nil
}

func prepareProfileSwitch(in *ProfileSwitchInputs, edits []ProfileVariableEdit) (ProfileSwitchPlan, *ProfileSwitchPreview, []configuration.CatalogFile, error) {
	if in.Desired.Catalog.Profile != configuration.XrayIdentityVersion {
		return ProfileSwitchPlan{}, nil, nil, ErrUnsupportedProfile
	}
	if in.Basis.ActiveGeneration == math.MaxInt64 || in.Basis.SlotGeneration == math.MaxInt64 {
		return ProfileSwitchPlan{}, nil, nil, ErrVersionExhausted
	}
	draft := routersettings.Draft{ID: in.Basis.RouterID, TemplateID: in.Basis.TargetTemplateID, Name: in.Current.Draft.Name}
	var basis *OrderBasis
	if in.TargetDraft != nil {
		draft = in.TargetDraft.Draft
		draft.Name = in.Current.Draft.Name
		basis = in.TargetDraft.OrderBasis
	}
	edits = ownProfileVariableEdits(edits)
	draft, err := editProfileVariables(in.Target, draft, edits)
	if err != nil {
		return ProfileSwitchPlan{}, nil, nil, err
	}
	if err := routersettings.ValidateDraft(draft); err != nil {
		return ProfileSwitchPlan{}, nil, nil, ErrInvalidCandidate
	}
	if err := checkXrayOnlyExclusions(in.Exclusions, draft); err != nil {
		return ProfileSwitchPlan{}, nil, nil, err
	}
	prepared, issues := PrepareRouterCandidate(in.Target, &draft, basis)
	if len(issues) != 0 {
		return ProfileSwitchPlan{}, nil, nil, &PreparationError{Issues: issues}
	}
	draft.PersonalOrder = prepared.PersonalOrder
	files := prepared.Catalog.Files
	plan := ProfileSwitchPlan{Expected: in.Basis, Settings: draft, OrderBasis: prepared.OrderBasis}
	if !sameCatalogFiles(in.Desired.Catalog.Files, files) {
		if in.Basis.DesiredNumber == math.MaxInt64 {
			return ProfileSwitchPlan{}, nil, nil, ErrVersionExhausted
		}
		plan.NewRevision = &VerifiedFiles{Profile: configuration.XrayIdentityVersion, Files: files}
	}
	changes, _ := catalogFileChanges(in.Desired.Catalog.Files, files)
	preview := &ProfileSwitchPreview{
		Proof:   ProfileSwitchProof{Basis: in.Basis, Fingerprint: fingerprintProfileCandidate(in.Basis, edits, prepared)},
		Changes: changes, Restoring: in.TargetDraft != nil, PersonalOrder: draft.PersonalOrder, OrderBasis: prepared.OrderBasis,
	}
	return plan, preview, files, nil
}
