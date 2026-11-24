package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
)

type WebRouterCatalog struct{ publications *Publications }

func NewWebRouterCatalog(publications *Publications) WebRouterCatalog {
	return WebRouterCatalog{publications: publications}
}

func (w WebRouterCatalog) ReadCatalog(ctx context.Context, digest [32]byte, clock func() time.Time, id string, mode publication.RouterPreviewMode) (*publication.Inputs, error) {
	return w.publications.readRouterCatalog(ctx, id, mode, webCatalogGuard(digest, clock))
}

func (w WebRouterCatalog) CheckCatalog(ctx context.Context, digest [32]byte, clock func() time.Time, mode publication.RouterPreviewMode, expected publication.Expectations) error {
	return w.publications.checkRouterCatalog(ctx, mode, expected, webCatalogGuard(digest, clock))
}

func webCatalogGuard(digest [32]byte, clock func() time.Time) publicationGuard {
	return func(ctx context.Context, tx *sql.Tx) error {
		return requireActiveWebSession(ctx, tx, activeWebSessionArgs(digest, clock()))
	}
}

func (p *Publications) readRouterCatalog(ctx context.Context, id string, mode publication.RouterPreviewMode, admission publicationGuard) (*publication.Inputs, error) {
	return p.read(ctx, publication.ReadRequest{RouterID: id, IncludeRouterDraft: mode == publication.PreviewPersonal}, func(ctx context.Context, tx *sql.Tx) error {
		if err := admission(ctx, tx); err != nil {
			return err
		}
		_, err := readVisibleRouterDraftBasis(ctx, tx, id)
		return err
	})
}

func (p *Publications) checkRouterCatalog(ctx context.Context, mode publication.RouterPreviewMode, expected publication.Expectations, admission publicationGuard) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := admission(ctx, tx); err != nil {
		return err
	}
	basis, err := readVisibleRouterDraftBasis(ctx, tx, expected.RouterID)
	if err != nil {
		return err
	}
	if basis.TemplateID != expected.TemplateID || basis.ActiveGeneration != expected.ActiveGeneration ||
		basis.AcceptedVersion != expected.AcceptedVersion || mode == publication.PreviewPersonal &&
		(expected.RouterDraftGeneration == nil || basis.DraftGeneration != *expected.RouterDraftGeneration) {
		return publication.ErrConflict
	}
	var desired int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(revision),0)
		FROM desired_router_revisions WHERE router_id=?`, basis.RouterID).Scan(&desired); err != nil {
		return err
	}
	if desired != expected.DesiredNumber {
		return publication.ErrConflict
	}
	return tx.Commit()
}
