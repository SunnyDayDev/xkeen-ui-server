package webadmin

import (
	"context"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

// TemplateSummary holds the metadata needed to display one saved draft.
type TemplateSummary = templates.Summary

// TemplateStore checks session activity in the same transaction as each operation.
type TemplateStore interface {
	List(context.Context, [32]byte, func() time.Time) ([]TemplateSummary, error)
	Create(context.Context, [32]byte, func() time.Time, configuration.TemplateDraft) error
}

type Templates struct {
	store TemplateStore
	now   func() time.Time
}

func NewTemplates(store TemplateStore, now func() time.Time) Templates {
	return Templates{store: store, now: now}
}

func (s Templates) List(ctx context.Context, token string) ([]TemplateSummary, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return nil, err
	}
	return s.store.List(ctx, digest, s.now)
}

func (s Templates) Create(ctx context.Context, token, proof, id string) (TemplateSummary, error) {
	digest, _, err := SessionProof(token, proof, true)
	if err != nil {
		return TemplateSummary{}, err
	}
	draft, err := templates.PrepareDraft(configuration.TemplateDraft{ID: id})
	if err != nil {
		return TemplateSummary{}, err
	}
	if err := s.store.Create(ctx, digest, s.now, draft); err != nil {
		return TemplateSummary{}, err
	}
	return TemplateSummary{ID: draft.ID, Version: 1}, nil
}
