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
	Version   int64
	Source    string
	Section   string
	ElementID string
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
	digest, _, err := SessionProof(token, csrf, true)
	if err != nil {
		return nil, "", err
	}
	if id == "" || !utf8.ValidString(id) || input.Version < 1 || (input.Section == "") == (input.ElementID == "") {
		return nil, "", templates.ErrInvalid
	}
	base, err := s.store.ReadTemplate(ctx, digest, s.now, id)
	if err != nil {
		return nil, "", err
	}
	if base.Version != input.Version {
		return nil, "", templates.ErrVersionConflict
	}
	elementID := input.ElementID
	var draft configuration.TemplateDraft
	if input.Section == "" {
		draft, err = templates.EditElementContent(base, elementID, input.Source)
	} else {
		newID, randomErr := uuid.NewRandom()
		if randomErr != nil {
			return nil, "", randomErr
		}
		elementID = newID.String()
		draft, err = templates.CreateFirstElement(base, input.Section, elementID, input.Source)
	}
	if err != nil {
		return nil, "", err
	}
	version, err := s.store.ReplaceTemplate(ctx, digest, s.now, draft, input.Version)
	if err != nil {
		return nil, "", err
	}
	return &templates.Snapshot{TemplateDraft: draft, Version: version}, elementID, nil
}
