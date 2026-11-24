// Package routerdraft holds transport-independent operations on one guarded router draft.
package routerdraft

import (
	"errors"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

var (
	ErrMissing  = errors.New("router unavailable in the overview")
	ErrConflict = errors.New("router draft basis changed")
	ErrForeign  = errors.New("router draft selects another template")
)

type Basis struct {
	RouterID, TemplateID              string
	AcceptedVersion, ActiveGeneration int64
	DraftGeneration                   int64 // Zero identifies an absent C04 draft.
}

type Snapshot struct {
	Exclusions *configuration.ExclusionSource
	Basis      Basis
	Name       string
	Accepted   *templates.Snapshot
	OrderBasis *publication.OrderBasis
	Editable   routersettings.Draft
}

func SharedElement(s Snapshot, id string) (configuration.Element, string, error) {
	if s.Editable.TemplateID != s.Basis.TemplateID {
		return configuration.Element{}, "", ErrForeign
	}
	if s.Accepted != nil && configuration.ValidElementID(id) {
		for _, section := range s.Accepted.Sections {
			for _, item := range section.Elements {
				if item.Element.ID == id {
					return item.Element, section.Name, nil
				}
			}
		}
	}
	return configuration.Element{}, "", ErrMissing
}
