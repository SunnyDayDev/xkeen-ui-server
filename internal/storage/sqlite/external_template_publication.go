package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

type ExternalTemplatePublications struct{ publications *Publications }

func NewExternalTemplatePublications(publications *Publications) ExternalTemplatePublications {
	return ExternalTemplatePublications{publications: publications}
}

type externalTemplateGroup struct {
	publications *Publications
	permit       externalaccess.Permit
}

func (w ExternalTemplatePublications) ForExternalTemplateGroup(permit externalaccess.Permit) publication.TemplateGroupStore {
	return externalTemplateGroup{publications: w.publications, permit: permit}
}

func (w ExternalTemplatePublications) CheckExternalTemplateGroupPreview(ctx context.Context, permit externalaccess.Permit, clock func() time.Time, preview *publication.TemplateGroupPreview) ([]reporting.Presence, error) {
	return w.publications.checkTemplateGroupPreview(ctx, clock, preview, func(ctx context.Context, tx *sql.Tx, _ time.Time) error {
		return requireExternalPermit(ctx, tx, permit)
	})
}

func (s externalTemplateGroup) ReadTemplateGroup(ctx context.Context, id string) (*publication.TemplateGroupInputs, error) {
	return s.publications.readTemplateGroup(ctx, id, func(ctx context.Context, tx *sql.Tx) error {
		err := requireExternalPermit(ctx, tx, s.permit)
		if errors.Is(err, externalaccess.ErrDisabled) || errors.Is(err, externalaccess.ErrUnauthorized) {
			return publication.ErrEffectRefused
		}
		return err
	})
}

func (s externalTemplateGroup) CommitTemplateGroup(context.Context, publication.TemplateGroupPlan) (*publication.TemplateGroupResult, error) {
	return nil, publication.ErrEffectRefused
}
