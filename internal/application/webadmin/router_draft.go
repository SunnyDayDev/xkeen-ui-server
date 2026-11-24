package webadmin

import (
	"context"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

var (
	ErrRouterDraftMissing  = routerdraft.ErrMissing
	ErrRouterDraftConflict = routerdraft.ErrConflict
	ErrRouterDraftForeign  = routerdraft.ErrForeign
)

type RouterDraftBasis = routerdraft.Basis
type RouterDraftSnapshot = routerdraft.Snapshot

type RouterDraftStore interface {
	Read(context.Context, [32]byte, func() time.Time, string) (RouterDraftSnapshot, error)
	Write(context.Context, [32]byte, func() time.Time, RouterDraftBasis, routersettings.Draft) (int64, error)
}
