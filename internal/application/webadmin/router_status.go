package webadmin

import (
	"context"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

type RouterStatus = reporting.RouterStatus

type RouterStatusStore interface {
	List(context.Context, [32]byte, func() time.Time) ([]RouterStatus, error)
}

type RouterOverview struct {
	store RouterStatusStore
	now   func() time.Time
}

func NewRouterOverview(store RouterStatusStore, now func() time.Time) RouterOverview {
	return RouterOverview{store: store, now: now}
}

func (s RouterOverview) List(ctx context.Context, token string) ([]RouterStatus, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return nil, err
	}
	return s.store.List(ctx, digest, s.now)
}
