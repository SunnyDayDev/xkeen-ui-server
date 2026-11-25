package webadmin

import (
	"context"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

type RouterVisibilityStore interface {
	Read(context.Context, [32]byte, func() time.Time, string) (routersettings.VisibilitySnapshot, error)
	Hide(context.Context, [32]byte, func() time.Time, routersettings.VisibilitySnapshot) (routersettings.VisibilitySnapshot, error)
}

type RouterVisibility struct {
	store RouterVisibilityStore
	now   func() time.Time
}

func NewRouterVisibility(store RouterVisibilityStore, now func() time.Time) RouterVisibility {
	return RouterVisibility{store: store, now: now}
}

func (s RouterVisibility) Read(ctx context.Context, token, id string) (routersettings.VisibilitySnapshot, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return routersettings.VisibilitySnapshot{}, err
	}
	return s.store.Read(ctx, digest, s.now, id)
}

func (s RouterVisibility) Hide(ctx context.Context, token, csrf string, expected routersettings.VisibilitySnapshot) (routersettings.VisibilitySnapshot, error) {
	digest, _, err := SessionProof(token, csrf, true)
	if err != nil {
		return routersettings.VisibilitySnapshot{}, err
	}
	return s.store.Hide(ctx, digest, s.now, expected)
}
