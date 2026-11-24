package templates

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

var (
	ErrConflict = errors.New("template already exists")
	ErrNotFound = errors.New("template not found")
	ErrCorrupt  = errors.New("invalid stored template draft")
	ErrInvalid  = errors.New("invalid template draft")
)

type Repository interface {
	Insert(context.Context, configuration.TemplateDraft) error
	Find(context.Context, string) (*configuration.TemplateDraft, error)
}

type Service struct{ repository Repository }

func New(repository Repository) Service { return Service{repository: repository} }

func (s Service) Create(ctx context.Context, draft configuration.TemplateDraft) error {
	prepared, err := copyDraft(draft, prepareElement)
	if err != nil {
		return err
	}
	return s.repository.Insert(ctx, prepared)
}

func (s Service) Get(ctx context.Context, id string) (*configuration.TemplateDraft, error) {
	if id == "" || !utf8.ValidString(id) {
		return nil, ErrInvalid
	}
	draft, err := s.repository.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if draft == nil || draft.ID != id {
		return nil, ErrCorrupt
	}
	restored, err := copyDraft(*draft, configuration.RestoreElement)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return &restored, nil
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
