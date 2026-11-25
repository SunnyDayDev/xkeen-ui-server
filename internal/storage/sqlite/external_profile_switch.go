package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
)

type ExternalProfileSwitches struct{ publications *Publications }

func NewExternalProfileSwitches(p *Publications) ExternalProfileSwitches {
	return ExternalProfileSwitches{p}
}

func (s ExternalProfileSwitches) CheckExternalSwitchPermit(ctx context.Context, permit externalaccess.Permit) error {
	tx, err := s.publications.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return requireExternalPermit(ctx, tx, permit)
}

type externalSwitchStore struct {
	publications *Publications
	permit       externalaccess.Permit
	router       string
	clock        func() time.Time
	expires      time.Time
}

func (s ExternalProfileSwitches) ForExternalSwitch(p externalaccess.Permit, id string, clock func() time.Time, expires time.Time) publication.ProfileSwitchStore {
	return externalSwitchStore{s.publications, p, id, clock, expires}
}
func (s externalSwitchStore) guard(ctx context.Context, tx *sql.Tx) error {
	if err := requireExternalPermit(ctx, tx, s.permit); err != nil {
		if errors.Is(err, externalaccess.ErrDisabled) || errors.Is(err, externalaccess.ErrUnauthorized) {
			return publication.ErrEffectRefused
		}
		return err
	}
	if _, err := readVisibleRouterDraftBasis(ctx, tx, s.router); err != nil {
		if errors.Is(err, routerdraft.ErrMissing) {
			return publication.ErrSourceMissing
		}
		return err
	}
	if !s.expires.IsZero() && !s.clock().Before(s.expires) {
		return publication.ErrConflict
	}
	return nil
}
func (s externalSwitchStore) ReadProfileSwitch(ctx context.Context, id, target string) (*publication.ProfileSwitchInputs, error) {
	if id != s.router {
		return nil, publication.ErrSourceMissing
	}
	return s.publications.readProfileSwitch(ctx, id, target, s.guard)
}
func (s externalSwitchStore) CommitProfileSwitch(ctx context.Context, plan publication.ProfileSwitchPlan) (int64, error) {
	if plan.Expected.RouterID != s.router || s.expires.IsZero() {
		return 0, publication.ErrEffectRefused
	}
	return s.publications.commitProfileSwitch(ctx, plan, s.guard)
}
