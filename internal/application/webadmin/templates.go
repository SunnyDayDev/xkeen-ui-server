package webadmin

import (
	"context"
	"crypto/sha256"
	"time"
)

// TemplateSummary holds the metadata needed to display one saved draft.
type TemplateSummary struct {
	ID      string
	Version int64
}

// TemplateListStore authorizes the list and reads it in the same snapshot.
type TemplateListStore interface {
	List(context.Context, [32]byte, func() time.Time) ([]TemplateSummary, error)
}

type TemplateLister struct {
	store TemplateListStore
	now   func() time.Time
}

func NewTemplateLister(store TemplateListStore, now func() time.Time) TemplateLister {
	return TemplateLister{store: store, now: now}
}

func (l TemplateLister) List(ctx context.Context, token string) ([]TemplateSummary, error) {
	raw, ok := decodeToken(token)
	if !ok {
		return nil, ErrSignedOut
	}
	return l.store.List(ctx, sha256.Sum256(raw), l.now)
}
