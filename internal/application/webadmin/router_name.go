package webadmin

import (
	"context"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

type RouterNameStore interface {
	Read(context.Context, [32]byte, func() time.Time, string) (routersettings.DisplayNameSnapshot, error)
	Save(context.Context, [32]byte, func() time.Time, routersettings.DisplayNameSnapshot, string) (routersettings.DisplayNameSnapshot, error)
}

type RouterName struct {
	store RouterNameStore
	now   func() time.Time
}

func NewRouterName(store RouterNameStore, now func() time.Time) RouterName {
	return RouterName{store: store, now: now}
}

func (s RouterName) Read(ctx context.Context, token, id string) (routersettings.DisplayNameSnapshot, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	return s.store.Read(ctx, digest, s.now, id)
}

func (s RouterName) Save(ctx context.Context, token, csrf string, expected routersettings.DisplayNameSnapshot, name string) (routersettings.DisplayNameSnapshot, error) {
	digest, _, err := SessionProof(token, csrf, true)
	if err != nil {
		return routersettings.DisplayNameSnapshot{}, err
	}
	return s.store.Save(ctx, digest, s.now, expected, name)
}
