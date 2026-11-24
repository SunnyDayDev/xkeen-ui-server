package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type WebRouterPreparation struct{ publications *Publications }

func NewWebRouterPreparation(p *Publications) WebRouterPreparation {
	return WebRouterPreparation{publications: p}
}

func (w WebRouterPreparation) ReadSource(ctx context.Context, digest [32]byte, clock func() time.Time, id string) (webadmin.PreparationSource, error) {
	if !validPublicationID(id) {
		return webadmin.PreparationSource{}, webadmin.ErrInvalidPreparation
	}
	tx, err := w.publications.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return webadmin.PreparationSource{}, err
	}
	defer tx.Rollback()
	if err := requireActiveWebSession(ctx, tx, activeWebSessionArgs(digest, clock())); err != nil {
		return webadmin.PreparationSource{}, err
	}
	return readPreparationSource(ctx, tx, id)
}

func readPreparationSource(ctx context.Context, tx *sql.Tx, id string) (webadmin.PreparationSource, error) {
	accepted, err := readPublicationTemplate(ctx, tx, readAcceptedTemplate, id)
	if err != nil {
		return webadmin.PreparationSource{}, err
	}
	if accepted != nil {
		return webadmin.PreparationSource{Kind: webadmin.PreparationAccepted, Snapshot: accepted}, nil
	}
	var alreadyAssigned bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM active_router_publications WHERE template_id = ?)", id).Scan(&alreadyAssigned); err != nil {
		return webadmin.PreparationSource{}, err
	}
	if alreadyAssigned {
		return webadmin.PreparationSource{}, publication.ErrStoredState
	}
	draft, err := readPublicationTemplate(ctx, tx, readTemplateDraft, id)
	if err != nil {
		return webadmin.PreparationSource{}, err
	}
	if draft == nil {
		return webadmin.PreparationSource{}, webadmin.ErrPreparationSourceMissing
	}
	return webadmin.PreparationSource{Kind: webadmin.PreparationDraft, Snapshot: draft}, nil
}

func (w WebRouterPreparation) ReadPrepared(ctx context.Context, digest [32]byte, clock func() time.Time, id string) (webadmin.PreparedRouter, error) {
	if !validPublicationID(id) {
		return webadmin.PreparedRouter{}, webadmin.ErrInvalidPreparation
	}
	tx, err := w.publications.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return webadmin.PreparedRouter{}, err
	}
	defer tx.Rollback()
	if err := requireActiveWebSession(ctx, tx, activeWebSessionArgs(digest, clock())); err != nil {
		return webadmin.PreparedRouter{}, err
	}
	active, err := readActivePublication(ctx, tx, id)
	if err != nil {
		return webadmin.PreparedRouter{}, err
	}
	if active == nil {
		return webadmin.PreparedRouter{}, webadmin.ErrPreparedRouterMissing
	}
	var number int64
	if err := tx.QueryRowContext(ctx, "SELECT MIN(revision) FROM desired_router_revisions WHERE router_id = ?", id).Scan(&number); err != nil || number != 1 {
		return webadmin.PreparedRouter{}, publication.ErrStoredState
	}
	name, err := displayedRouterName(ctx, tx, id, active.Draft.Name)
	if err != nil {
		return webadmin.PreparedRouter{}, err
	}
	return webadmin.PreparedRouter{RouterID: id, Name: name, TemplateID: active.Draft.TemplateID, Revision: number}, nil
}

type guardedWebPublication struct {
	base   *Publications
	digest [32]byte
	clock  func() time.Time
}

func (w WebRouterPreparation) ForPublication(digest [32]byte, clock func() time.Time) publication.Store {
	return guardedWebPublication{base: w.publications, digest: digest, clock: clock}
}

func (s guardedWebPublication) Read(ctx context.Context, request publication.ReadRequest) (*publication.Inputs, error) {
	return s.base.read(ctx, request, func(ctx context.Context, tx *sql.Tx) error {
		err := requireActiveWebSession(ctx, tx, activeWebSessionArgs(s.digest, s.clock()))
		if errors.Is(err, webadmin.ErrSignedOut) {
			return publication.ErrEffectRefused
		}
		return err
	})
}

func (s guardedWebPublication) Commit(ctx context.Context, plan publication.CommitPlan) (int64, error) {
	return s.base.commit(ctx, plan, func(ctx context.Context, tx *sql.Tx) error {
		now := s.clock()
		err := touchActiveWebSession(ctx, tx, now, activeWebSessionArgs(s.digest, now))
		if errors.Is(err, webadmin.ErrSignedOut) {
			return publication.ErrEffectRefused
		}
		return err
	})
}
