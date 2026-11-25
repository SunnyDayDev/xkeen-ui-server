package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type WebRouterProfiles struct{ publications *Publications }

func NewWebRouterProfiles(publications *Publications) WebRouterProfiles {
	return WebRouterProfiles{publications: publications}
}

func (w WebRouterProfiles) ForProfileSwitch(digest [32]byte, clock func() time.Time, expires time.Time) publication.ProfileSwitchStore {
	return webProfileAccess{w.publications, digest, clock, expires}
}

type webProfileAccess struct {
	base    *Publications
	digest  [32]byte
	clock   func() time.Time
	expires time.Time
}

func (s webProfileAccess) ReadProfileSwitch(ctx context.Context, id, target string) (*publication.ProfileSwitchInputs, error) {
	return s.base.readProfileSwitch(ctx, id, target, s.admit(id, false))
}

func (s webProfileAccess) CommitProfileSwitch(ctx context.Context, plan publication.ProfileSwitchPlan) (int64, error) {
	return s.base.commitProfileSwitch(ctx, plan, s.admit(plan.Expected.RouterID, true))
}

func (s webProfileAccess) admit(id string, write bool) publicationGuard {
	return func(ctx context.Context, tx *sql.Tx) error {
		now := s.clock()
		args := activeWebSessionArgs(s.digest, now)
		var err error
		if write {
			if !now.Before(s.expires) {
				return publication.ErrConflict
			}
			err = touchActiveWebSession(ctx, tx, now, args)
		} else {
			err = requireActiveWebSession(ctx, tx, args)
		}
		if errors.Is(err, webadmin.ErrSignedOut) {
			return publication.ErrEffectRefused
		}
		if err != nil {
			return err
		}
		_, err = readVisibleRouterDraftBasis(ctx, tx, id)
		if errors.Is(err, routerdraft.ErrMissing) {
			if write {
				return publication.ErrConflict
			}
			return publication.ErrSourceMissing
		}
		return err
	}
}
