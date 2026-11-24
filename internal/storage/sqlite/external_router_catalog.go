package sqlite

import (
	"context"
	"database/sql"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
)

func (s *ExternalAccess) ReadExternalRouterCatalog(ctx context.Context, permit externalaccess.Permit, id string, mode publication.RouterPreviewMode) (*publication.Inputs, error) {
	return NewPublications(s.db).readRouterCatalog(ctx, id, mode, externalCatalogGuard(permit))
}

func (s *ExternalAccess) CheckExternalRouterCatalog(ctx context.Context, permit externalaccess.Permit, mode publication.RouterPreviewMode, expected publication.Expectations) error {
	return NewPublications(s.db).checkRouterCatalog(ctx, mode, expected, externalCatalogGuard(permit))
}

func externalCatalogGuard(permit externalaccess.Permit) publicationGuard {
	return func(ctx context.Context, tx *sql.Tx) error {
		return requireExternalPermit(ctx, tx, permit)
	}
}
