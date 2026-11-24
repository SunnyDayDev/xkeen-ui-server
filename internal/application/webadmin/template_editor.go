package webadmin

import (
	"context"
	"time"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/google/uuid"
)

type TemplateEditorStore interface {
	ReadTemplate(context.Context, [32]byte, func() time.Time, string) (*templates.Snapshot, error)
	ReplaceTemplate(context.Context, [32]byte, func() time.Time, configuration.TemplateDraft, int64) (int64, error)
}

type TemplateEditor struct {
	store TemplateEditorStore
	now   func() time.Time
}

func NewTemplateEditor(store TemplateEditorStore, now func() time.Time) TemplateEditor {
	return TemplateEditor{store: store, now: now}
}

type TemplateElementEdit struct {
	Version       int64
	Source        string
	Section       string
	AppendSection string
	ElementID     string
}

func (s TemplateEditor) Read(ctx context.Context, token, id string) (*templates.Snapshot, error) {
	if id == "" || !utf8.ValidString(id) {
		return nil, templates.ErrInvalid
	}
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return nil, err
	}
	return s.store.ReadTemplate(ctx, digest, s.now, id)
}

func (s TemplateEditor) Save(ctx context.Context, token, csrf, id string, input TemplateElementEdit) (*templates.Snapshot, string, error) {
	if (input.ElementID == "" && input.Section == "" && input.AppendSection == "") ||
		(input.ElementID != "" && (input.Section != "" || input.AppendSection != "")) ||
		(input.Section != "" && input.AppendSection != "") {
		return nil, "", templates.ErrInvalid
	}
	base, digest, err := s.editBase(ctx, token, csrf, id, input.Version)
	if err != nil {
		return nil, "", err
	}
	elementID := input.ElementID
	var draft configuration.TemplateDraft
	if elementID != "" {
		draft, err = templates.EditElementContent(base, elementID, input.Source)
	} else {
		newID, randomErr := uuid.NewRandom()
		if randomErr != nil {
			return nil, "", randomErr
		}
		elementID = newID.String()
		if input.AppendSection != "" {
			draft, err = templates.AppendElement(base, input.AppendSection, elementID, input.Source)
		} else {
			draft, err = templates.CreateFirstElement(base, input.Section, elementID, input.Source)
		}
	}
	if err != nil {
		return nil, "", err
	}
	saved, err := s.replace(ctx, digest, draft, input.Version)
	if err != nil {
		return nil, "", err
	}
	return saved, elementID, nil
}

func (s TemplateEditor) SaveDefinitions(ctx context.Context, token, csrf, id, elementID string, expectedVersion int64, definitions []configuration.VariableDefinition) (*templates.Snapshot, error) {
	base, digest, err := s.editBase(ctx, token, csrf, id, expectedVersion)
	if err != nil {
		return nil, err
	}
	candidate, err := templates.EditElementDefinitions(base, elementID, definitions)
	if err != nil {
		return nil, err
	}
	return s.replace(ctx, digest, candidate, expectedVersion)
}

func (s TemplateEditor) editBase(ctx context.Context, token, csrf, id string, expectedVersion int64) (*templates.Snapshot, [32]byte, error) {
	digest, _, err := SessionProof(token, csrf, true)
	if err != nil {
		return nil, digest, err
	}
	if id == "" || !utf8.ValidString(id) || expectedVersion < 1 {
		return nil, digest, templates.ErrInvalid
	}
	base, err := s.store.ReadTemplate(ctx, digest, s.now, id)
	if err != nil {
		return nil, digest, err
	}
	if base.Version != expectedVersion {
		return nil, digest, templates.ErrVersionConflict
	}
	return base, digest, nil
}

func (s TemplateEditor) replace(ctx context.Context, digest [32]byte, draft configuration.TemplateDraft, expectedVersion int64) (*templates.Snapshot, error) {
	version, err := s.store.ReplaceTemplate(ctx, digest, s.now, draft, expectedVersion)
	if err != nil {
		return nil, err
	}
	return &templates.Snapshot{TemplateDraft: draft, Version: version}, nil
}
