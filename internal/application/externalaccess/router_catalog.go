package externalaccess

import (
	"context"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
)

type RouterCatalogStore interface {
	ReadExternalRouterCatalog(context.Context, Permit, string, publication.RouterPreviewMode) (*publication.Inputs, error)
	CheckExternalRouterCatalog(context.Context, Permit, publication.RouterPreviewMode, publication.Expectations) error
}

type RouterCatalog struct{ store RouterCatalogStore }

func NewRouterCatalog(store RouterCatalogStore) RouterCatalog { return RouterCatalog{store: store} }

func (s RouterCatalog) Read(ctx context.Context, permit Permit, id string, mode publication.RouterPreviewMode) (*publication.RouterCatalogPreview, error) {
	inputs, err := s.store.ReadExternalRouterCatalog(ctx, permit, id, mode)
	if err != nil {
		return nil, err
	}
	return publication.ScopedRouterPreview(inputs, mode, id, func(expected publication.Expectations) error {
		return s.store.CheckExternalRouterCatalog(ctx, permit, mode, expected)
	})
}
