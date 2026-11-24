package publication

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type SharedSource string

const (
	SourceSharedDraft     SharedSource = "shared_draft"
	SourceSharedCandidate SharedSource = "shared_candidate"
)

type SharedPublishRequest struct {
	TemplateID                            string
	TemplateDraftVersion, AcceptedVersion int64
	Source                                SharedSource
	Candidate                             *configuration.TemplateDraft
	// Nil means no prior preview. A non-nil empty slice expects no assignments.
	PreviewMembers     []GroupRouterExpectation
	PreviewFingerprint *[32]byte
}

type TemplateGroupService struct {
	store     TemplateGroupStore
	validator CatalogValidator
}

func NewTemplateGroupService(store TemplateGroupStore, validator CatalogValidator) *TemplateGroupService {
	return &TemplateGroupService{store: store, validator: validator}
}

// Publish validates every affected catalog before issuing a single group commit.
func (s *TemplateGroupService) Publish(ctx context.Context, request SharedPublishRequest) (*TemplateGroupResult, error) {
	plan, _, err := s.prepare(ctx, request, false)
	if err != nil {
		return nil, err
	}
	if request.PreviewFingerprint != nil && fingerprintGroupPlan(plan) != *request.PreviewFingerprint {
		return nil, ErrConflict
	}
	result, err := s.store.CommitTemplateGroup(ctx, plan)
	if err != nil {
		return nil, safeStoreError(err, ErrPublicationCommit)
	}
	if result == nil {
		return nil, ErrPublicationCommit
	}
	return result, nil
}
func (s *TemplateGroupService) prepare(ctx context.Context, request SharedPublishRequest, dryRun bool) (TemplateGroupPlan, []GroupMemberPreview, error) {
	if !validSharedRequest(request) {
		return TemplateGroupPlan{}, nil, ErrInvalidCommand
	}
	if err := ctx.Err(); err != nil {
		return TemplateGroupPlan{}, nil, err
	}
	preview := slices.Clone(request.PreviewMembers)
	edit, err := prepareSharedEdit(request)
	if err != nil {
		return TemplateGroupPlan{}, nil, err
	}
	inputs, err := s.store.ReadTemplateGroup(ctx, request.TemplateID)
	if err != nil {
		if errors.Is(err, ErrSourceMissing) {
			return TemplateGroupPlan{}, nil, ErrSourceMissing
		}
		return TemplateGroupPlan{}, nil, safeStoreError(err, ErrPublicationRead)
	}
	if inputs == nil || inputs.TemplateDraft == nil {
		return TemplateGroupPlan{}, nil, ErrPublicationRead
	}
	if dryRun && inputs.Accepted != nil {
		request.AcceptedVersion = inputs.Accepted.Version
	}
	if !matchesSharedVersions(inputs, request) {
		return TemplateGroupPlan{}, nil, ErrConflict
	}
	members, expectations, err := sharedMembers(inputs, request.TemplateID)
	if err != nil {
		return TemplateGroupPlan{}, nil, err
	}
	if preview != nil && !matchesGroupPreview(preview, expectations) {
		return TemplateGroupPlan{}, nil, ErrConflict
	}

	selected := inputs.TemplateDraft
	if edit != nil {
		selected = &templates.Snapshot{TemplateDraft: *edit, Version: request.TemplateDraftVersion + 1}
	}
	plan := TemplateGroupPlan{TemplateID: request.TemplateID, TemplateDraftVersion: request.TemplateDraftVersion,
		AcceptedVersion: request.AcceptedVersion, Edit: edit, Members: make([]GroupRouterPlan, 0, len(members))}
	var views []GroupMemberPreview
	if dryRun {
		views = make([]GroupMemberPreview, 0, len(members))
	}
	for i, member := range members {
		if err := checkXrayOnlyExclusions(inputs.Exclusions, member.Active.Draft); err != nil {
			return TemplateGroupPlan{}, nil, &GroupMemberError{RouterID: expectations[i].RouterID, Cause: err}
		}
		candidate, err := prepareGroupMember(ctx, s.validator, selected, member)
		if err != nil {
			return TemplateGroupPlan{}, nil, &GroupMemberError{RouterID: expectations[i].RouterID, Cause: err}
		}
		candidate.Expected = expectations[i]
		if candidate.NewRevision != nil && candidate.Expected.DesiredNumber == math.MaxInt64 {
			return TemplateGroupPlan{}, nil, ErrVersionExhausted
		}
		plan.Members = append(plan.Members, candidate)
		if dryRun {
			views = append(views, previewGroupMember(member, candidate))
		}
	}
	return plan, views, nil
}

func validSharedRequest(request SharedPublishRequest) bool {
	if !validIdentity(request.TemplateID) || request.TemplateDraftVersion < 1 ||
		(request.PreviewFingerprint != nil && request.Source != SourceSharedDraft) ||
		request.AcceptedVersion < 0 || request.AcceptedVersion > request.TemplateDraftVersion {
		return false
	}
	switch request.Source {
	case SourceSharedDraft:
		return request.Candidate == nil
	case SourceSharedCandidate:
		return request.Candidate != nil && request.Candidate.ID == request.TemplateID
	default:
		return false
	}
}

func prepareSharedEdit(request SharedPublishRequest) (*configuration.TemplateDraft, error) {
	if request.Candidate == nil {
		return nil, nil
	}
	if request.TemplateDraftVersion == math.MaxInt64 {
		return nil, ErrVersionExhausted
	}
	prepared, err := templates.PrepareDraft(*request.Candidate)
	if err != nil {
		return nil, err
	}
	return &prepared, nil
}

func matchesSharedVersions(inputs *TemplateGroupInputs, request SharedPublishRequest) bool {
	var acceptedVersion int64
	if inputs.Accepted != nil {
		if inputs.Accepted.ID != request.TemplateID {
			return false
		}
		acceptedVersion = inputs.Accepted.Version
	}
	return inputs.TemplateDraft.ID == request.TemplateID && inputs.TemplateDraft.Version == request.TemplateDraftVersion &&
		acceptedVersion == request.AcceptedVersion
}

func sharedMembers(inputs *TemplateGroupInputs, templateID string) ([]TemplateGroupMember, []GroupRouterExpectation, error) {
	members := slices.Clone(inputs.Members)
	if inputs.Accepted == nil && len(members) != 0 {
		return nil, nil, ErrStoredState
	}
	slices.SortFunc(members, func(a, b TemplateGroupMember) int {
		return strings.Compare(a.Active.Draft.ID, b.Active.Draft.ID)
	})
	expectations := make([]GroupRouterExpectation, len(members))
	for i, member := range members {
		id := member.Active.Draft.ID
		if !validIdentity(id) || member.Active.Draft.TemplateID != templateID ||
			member.Active.Generation < 1 || member.Desired.Number < 1 ||
			(i > 0 && id == expectations[i-1].RouterID) {
			return nil, nil, ErrStoredState
		}
		expectations[i] = GroupRouterExpectation{RouterID: id, ActiveGeneration: member.Active.Generation, DesiredNumber: member.Desired.Number}
	}
	return members, expectations, nil
}

func matchesGroupPreview(preview, current []GroupRouterExpectation) bool {
	slices.SortFunc(preview, func(a, b GroupRouterExpectation) int {
		return strings.Compare(a.RouterID, b.RouterID)
	})
	return slices.Equal(preview, current)
}

func prepareGroupMember(ctx context.Context, validator CatalogValidator, shared *templates.Snapshot, member TemplateGroupMember) (GroupRouterPlan, error) {
	if member.Desired.Catalog.Profile != configuration.XrayIdentityVersion {
		return GroupRouterPlan{}, ErrUnsupportedProfile
	}
	if err := routersettings.ValidateDraft(member.Active.Draft); err != nil {
		return GroupRouterPlan{}, err
	}
	candidate, issues := PrepareRouterCandidate(shared, &member.Active.Draft, member.Active.OrderBasis)
	if len(issues) != 0 {
		return GroupRouterPlan{}, &PreparationError{Issues: issues}
	}
	files := candidate.Catalog.Files
	if err := validateCatalogFiles(ctx, validator, files); err != nil {
		return GroupRouterPlan{}, err
	}
	result := GroupRouterPlan{PersonalOrder: candidate.PersonalOrder, OrderBasis: candidate.OrderBasis}
	if !sameCatalogFiles(member.Desired.Catalog.Files, files) {
		result.NewRevision = &VerifiedFiles{Profile: configuration.XrayIdentityVersion, Files: files}
	}
	return result, nil
}

// GroupMemberError identifies the failed participant while excluding authored data.
type GroupMemberError struct {
	RouterID string
	Cause    error
}

func (e *GroupMemberError) Error() string { return "could not publish member" }
func (e *GroupMemberError) Unwrap() error { return e.Cause }
