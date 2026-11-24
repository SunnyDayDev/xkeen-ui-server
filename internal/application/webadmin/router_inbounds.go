package webadmin

import (
	"context"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type RouterInboundEditor struct {
	Store RouterDraftStore
	Now   func() time.Time
}

type RouterInboundView struct {
	Basis        RouterDraftBasis
	Name         string
	Items        []publication.DraftInbound
	Selected     string
	Dependencies []publication.InboundDependency
}

func (e RouterInboundEditor) Read(ctx context.Context, token, routerID, selected string) (RouterInboundView, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return RouterInboundView{}, err
	}
	snapshot, err := e.Store.Read(ctx, digest, e.Now, routerID)
	if err != nil {
		return RouterInboundView{}, err
	}
	prepared, err := prepareEditableInbounds(snapshot)
	if err != nil {
		return RouterInboundView{}, err
	}
	items, selected, deps, err := prepared.Inspect(selected)
	if err != nil {
		return RouterInboundView{}, err
	}
	return RouterInboundView{Basis: snapshot.Basis, Name: snapshot.Name, Items: items, Selected: selected, Dependencies: deps}, nil
}

func (e RouterInboundEditor) Change(ctx context.Context, token, csrf string, basis RouterDraftBasis, id string, action configuration.InboundAction) (int64, error) {
	digest, _, err := SessionProof(token, csrf, true)
	if err != nil {
		return 0, err
	}
	snapshot, err := e.Store.Read(ctx, digest, e.Now, basis.RouterID)
	if err != nil {
		return 0, err
	}
	if snapshot.Basis != basis {
		return 0, ErrRouterDraftConflict
	}
	prepared, err := prepareEditableInbounds(snapshot)
	if err != nil {
		return 0, err
	}
	draft, err := prepared.Change(id, action)
	if err != nil {
		return 0, err
	}
	return e.Store.Write(ctx, digest, e.Now, basis, draft)
}

func prepareEditableInbounds(snapshot RouterDraftSnapshot) (*publication.DraftInbounds, error) {
	if snapshot.Editable.TemplateID != snapshot.Basis.TemplateID {
		return nil, ErrRouterDraftForeign
	}
	return publication.PrepareDraftInbounds(snapshot.Accepted, snapshot.Editable, snapshot.OrderBasis)
}
