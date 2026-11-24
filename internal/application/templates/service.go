package templates

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

var (
	ErrConflict         = errors.New("template already exists")
	ErrNotFound         = errors.New("template not found")
	ErrCorrupt          = errors.New("invalid stored template draft")
	ErrInvalid          = errors.New("invalid template draft")
	ErrInvalidVersion   = fmt.Errorf("%w: expected version must be positive", ErrInvalid)
	ErrVersionConflict  = errors.New("version_conflict")
	ErrVersionExhausted = errors.New("template version exhausted")
)

// Snapshot is the complete saved draft and its version for this template ID.
type Snapshot struct {
	configuration.TemplateDraft
	Version int64
}

// Repository writes complete snapshots without retaining their input buffers.
// Replace checks and advances the version in the same commit as the draft.
type Repository interface {
	Insert(context.Context, configuration.TemplateDraft) error
	Find(context.Context, string) (*Snapshot, error)
	Replace(ctx context.Context, draft configuration.TemplateDraft, expectedVersion int64) (int64, error)
}

type Service struct{ repository Repository }

func New(repository Repository) Service { return Service{repository: repository} }

func (s Service) Create(ctx context.Context, draft configuration.TemplateDraft) (*Snapshot, error) {
	prepared, err := PrepareDraft(draft)
	if err != nil {
		return nil, err
	}
	if err := s.repository.Insert(ctx, prepared); err != nil {
		return nil, err
	}
	return &Snapshot{TemplateDraft: prepared, Version: 1}, nil
}

func (s Service) Get(ctx context.Context, id string) (*Snapshot, error) {
	if id == "" || !utf8.ValidString(id) {
		return nil, ErrInvalid
	}
	snapshot, err := s.repository.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	return RestoreSnapshot(id, snapshot)
}

// RestoreSnapshot validates and isolates a repository read, including reads
// performed under a channel's transaction-bound authorization check.
func RestoreSnapshot(id string, snapshot *Snapshot) (*Snapshot, error) {
	if snapshot == nil || snapshot.ID != id || snapshot.Version < 1 {
		return nil, ErrCorrupt
	}
	restored, err := copyDraft(snapshot.TemplateDraft, configuration.RestoreElement)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return &Snapshot{TemplateDraft: restored, Version: snapshot.Version}, nil
}

func (s Service) Replace(ctx context.Context, draft configuration.TemplateDraft, expectedVersion int64) (*Snapshot, error) {
	if expectedVersion < 1 {
		return nil, ErrInvalidVersion
	}
	prepared, err := PrepareDraft(draft)
	if err != nil {
		return nil, err
	}
	version, err := s.repository.Replace(ctx, prepared, expectedVersion)
	if err != nil {
		return nil, err
	}
	return &Snapshot{TemplateDraft: prepared, Version: version}, nil
}

// PrepareDraft validates a complete authored snapshot and prepares declarations
// without writing it. Create, Replace, and shared publication use the same path.
func PrepareDraft(draft configuration.TemplateDraft) (configuration.TemplateDraft, error) {
	return copyDraft(draft, prepareElement)
}

func prepareElement(element configuration.Element, location configuration.ElementLocation) (*configuration.Element, []configuration.ElementIssue) {
	return configuration.PrepareElement(element, location, nil)
}

func copyDraft(draft configuration.TemplateDraft, copyElement func(configuration.Element, configuration.ElementLocation) (*configuration.Element, []configuration.ElementIssue)) (configuration.TemplateDraft, error) {
	if issues := configuration.ValidateTemplateDraft(draft); len(issues) != 0 {
		return configuration.TemplateDraft{}, &InvalidDraftError{Issues: issues}
	}
	result := configuration.TemplateDraft{ID: draft.ID, Sections: make([]configuration.TemplateSection, len(draft.Sections))}
	for i, section := range draft.Sections {
		result.Sections[i] = configuration.TemplateSection{Name: section.Name, Elements: make([]configuration.TemplateElement, len(section.Elements))}
		for j, item := range section.Elements {
			element, issues := copyElement(item.Element, draft.ElementLocation(i, j))
			if len(issues) != 0 {
				return configuration.TemplateDraft{}, &InvalidDraftError{Issues: issues}
			}
			result.Sections[i].Elements[j] = configuration.TemplateElement{Element: *element, Disabled: item.Disabled}
		}
	}
	return result, nil
}

// InvalidDraftError locates invalid records without including authored values.
type InvalidDraftError struct{ Issues []configuration.ElementIssue }

func (e *InvalidDraftError) Error() string { return ErrInvalid.Error() }
func (e *InvalidDraftError) Unwrap() error { return ErrInvalid }
