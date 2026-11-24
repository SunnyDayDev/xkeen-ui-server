package webadmin

import (
	"context"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
)

type RouterCatalogPreviewStore interface {
	ReadCatalog(context.Context, [32]byte, func() time.Time, string, publication.RouterPreviewMode) (*publication.Inputs, error)
	CheckCatalog(context.Context, [32]byte, func() time.Time, publication.RouterPreviewMode, publication.Expectations) error
}

type RouterCatalogPreview struct {
	store RouterCatalogPreviewStore
	now   func() time.Time
}

func NewRouterCatalogPreview(store RouterCatalogPreviewStore, now func() time.Time) RouterCatalogPreview {
	return RouterCatalogPreview{store: store, now: now}
}

func (s RouterCatalogPreview) Read(ctx context.Context, token, routerID string, mode publication.RouterPreviewMode) (*publication.RouterCatalogPreview, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return nil, err
	}
	if mode != publication.PreviewDesired && mode != publication.PreviewPersonal {
		return nil, publication.ErrInvalidCommand
	}
	inputs, err := s.store.ReadCatalog(ctx, digest, s.now, routerID, mode)
	if err != nil {
		return nil, err
	}
	preview, err := publication.PreviewRouterFiles(inputs, mode)
	if err != nil {
		return nil, err
	}
	if preview.Expected.RouterID != routerID {
		return nil, publication.ErrStoredState
	}
	if err := s.store.CheckCatalog(ctx, digest, s.now, mode, preview.Expected); err != nil {
		return nil, err
	}
	return preview, nil
}
