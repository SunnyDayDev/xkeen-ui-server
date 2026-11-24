package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerpreparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type ExternalRouterPreparation struct{ publications *Publications }

func NewExternalRouterPreparation(p *Publications) ExternalRouterPreparation {
	return ExternalRouterPreparation{publications: p}
}

func (s ExternalRouterPreparation) ReadExternalPreparation(ctx context.Context, permit externalaccess.Permit, router, template string) (routerpreparation.Source, bool, error) {
	tx, err := s.publications.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return routerpreparation.Source{}, false, err
	}
	defer tx.Rollback()
	if err := requireExternalPermit(ctx, tx, permit); err != nil {
		return routerpreparation.Source{}, false, err
	}
	active, err := readActivePublication(ctx, tx, router)
	if err != nil {
		return routerpreparation.Source{}, false, err
	}
	if active != nil {
		if active.Draft.TemplateID != template {
			return routerpreparation.Source{}, false, externalaccess.ErrConflict
		}
		var number int64
		if err := tx.QueryRowContext(ctx, "SELECT MIN(revision) FROM desired_router_revisions WHERE router_id=?", router).Scan(&number); err != nil || number != 1 {
			return routerpreparation.Source{}, false, publication.ErrStoredState
		}
		return routerpreparation.Source{}, true, nil
	}
	source, err := readPreparationSource(ctx, tx, template)
	if errors.Is(err, webadmin.ErrPreparationSourceMissing) {
		return routerpreparation.Source{}, false, externalaccess.ErrPreparationMissing
	}
	return source, false, err
}

func (s ExternalRouterPreparation) ForExternalPreparation(permit externalaccess.Permit) publication.Store {
	return guardedExternalPreparation{publications: s.publications, permit: permit}
}

type guardedExternalPreparation struct {
	publications *Publications
	permit       externalaccess.Permit
}

func (s guardedExternalPreparation) admit(ctx context.Context, tx *sql.Tx) error {
	err := requireExternalPermit(ctx, tx, s.permit)
	if errors.Is(err, externalaccess.ErrDisabled) || errors.Is(err, externalaccess.ErrUnauthorized) {
		return publication.ErrEffectRefused
	}
	return err
}

func (s guardedExternalPreparation) Read(ctx context.Context, request publication.ReadRequest) (*publication.Inputs, error) {
	return s.publications.read(ctx, request, s.admit)
}

func (s guardedExternalPreparation) Commit(ctx context.Context, plan publication.CommitPlan) (int64, error) {
	return s.publications.commit(ctx, plan, s.admit)
}
