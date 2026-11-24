package webadmin

import (
	"context"
	"errors"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
)

var (
	ErrRouterDraftMissing  = errors.New("router unavailable in the overview")
	ErrRouterDraftConflict = errors.New("router draft basis changed")
	ErrRouterDraftForeign  = errors.New("router draft selects another template")
)

type RouterDraftBasis struct {
	RouterID, TemplateID              string
	AcceptedVersion, ActiveGeneration int64
	DraftGeneration                   int64 // Zero identifies an absent C04 draft.
}

type RouterDraftSnapshot struct {
	Basis      RouterDraftBasis
	Name       string
	Accepted   *templates.Snapshot
	OrderBasis *publication.OrderBasis
	Editable   routersettings.Draft // Existing C04 draft, or active settings as the initial source.
}

type RouterDraftStore interface {
	Read(context.Context, [32]byte, func() time.Time, string) (RouterDraftSnapshot, error)
	Write(context.Context, [32]byte, func() time.Time, RouterDraftBasis, routersettings.Draft) (int64, error)
}
