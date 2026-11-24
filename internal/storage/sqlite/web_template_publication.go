package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

type WebTemplatePublications struct{ publications *Publications }

func NewWebTemplatePublications(p *Publications) WebTemplatePublications {
	return WebTemplatePublications{publications: p}
}

type sessionTemplateGroup struct {
	publications *Publications
	digest       [32]byte
	clock        func() time.Time
	expires      time.Time
}

func (w WebTemplatePublications) ForTemplateGroup(digest [32]byte, clock func() time.Time, expires time.Time) publication.TemplateGroupStore {
	return sessionTemplateGroup{publications: w.publications, digest: digest, clock: clock, expires: expires}
}

func (s sessionTemplateGroup) ReadTemplateGroup(ctx context.Context, id string) (*publication.TemplateGroupInputs, error) {
	return s.publications.readTemplateGroup(ctx, id, func(ctx context.Context, tx *sql.Tx) error {
		return groupSessionError(requireActiveWebSession(ctx, tx, activeWebSessionArgs(s.digest, s.clock())))
	})
}

func (s sessionTemplateGroup) CommitTemplateGroup(ctx context.Context, plan publication.TemplateGroupPlan) (*publication.TemplateGroupResult, error) {
	return s.publications.commitTemplateGroup(ctx, plan, func(ctx context.Context, tx *sql.Tx) error {
		now := s.clock()
		if s.expires.IsZero() || !now.Before(s.expires) {
			return publication.ErrConflict
		}
		return groupSessionError(touchActiveWebSession(ctx, tx, now, activeWebSessionArgs(s.digest, now)))
	})
}

func (w WebTemplatePublications) CheckPreview(ctx context.Context, digest [32]byte, clock func() time.Time, preview *publication.TemplateGroupPreview) ([]reporting.Presence, error) {
	return w.publications.checkTemplateGroupPreview(ctx, clock, preview, func(ctx context.Context, tx *sql.Tx, now time.Time) error {
		return requireActiveWebSession(ctx, tx, activeWebSessionArgs(digest, now))
	})
}

func (p *Publications) checkTemplateGroupPreview(ctx context.Context, clock func() time.Time, preview *publication.TemplateGroupPreview, admission func(context.Context, *sql.Tx, time.Time) error) ([]reporting.Presence, error) {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := clock()
	if err := admission(ctx, tx, now); err != nil {
		return nil, err
	}
	if preview == nil {
		return nil, nil
	}
	plan := publication.TemplateGroupPlan{TemplateID: preview.TemplateID, TemplateDraftVersion: preview.Version, AcceptedVersion: preview.AcceptedVersion}
	for _, member := range preview.Members {
		plan.Members = append(plan.Members, publication.GroupRouterPlan{Expected: member.Expected})
	}
	if err := checkGroupExpectations(ctx, tx, plan, plan.Members); err != nil {
		return nil, err
	}
	status := make([]reporting.Presence, len(preview.Members))
	for i, member := range preview.Members {
		found, err := readContactStatus(ctx, tx, member.Expected.RouterID, now)
		if err != nil {
			return nil, err
		}
		status[i] = found.Presence
	}
	return status, nil
}

func groupSessionError(err error) error {
	if errors.Is(err, webadmin.ErrSignedOut) {
		return publication.ErrEffectRefused
	}
	return err
}
