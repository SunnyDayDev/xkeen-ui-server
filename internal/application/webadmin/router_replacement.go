package webadmin

import (
	"context"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type RouterReplacementEditor struct {
	store RouterDraftStore
	now   func() time.Time
}

func NewRouterReplacementEditor(store RouterDraftStore, now func() time.Time) RouterReplacementEditor {
	return RouterReplacementEditor{store: store, now: now}
}

type RouterReplacement struct {
	Basis RouterDraftBasis
	Name  string
	routerdraft.ReplacementSelection
}

func (e RouterReplacementEditor) Read(ctx context.Context, token, routerID, elementID string) (RouterReplacement, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return RouterReplacement{}, err
	}
	snapshot, err := e.store.Read(ctx, digest, e.now, routerID)
	if err != nil {
		return RouterReplacement{}, err
	}
	return readRouterReplacement(snapshot, elementID)
}

func (e RouterReplacementEditor) Save(ctx context.Context, token, csrf string, basis RouterDraftBasis, elementID string, action configuration.ContentAction) (RouterReplacement, error) {
	digest, _, err := SessionProof(token, csrf, true)
	if err != nil {
		return RouterReplacement{}, err
	}
	snapshot, err := e.store.Read(ctx, digest, e.now, basis.RouterID)
	if err != nil {
		return RouterReplacement{}, err
	}
	if snapshot.Basis != basis {
		return RouterReplacement{}, ErrRouterDraftConflict
	}
	snapshot.Editable, err = routerdraft.EditReplacement(snapshot, elementID, action)
	if err != nil {
		return RouterReplacement{}, err
	}
	result, err := readRouterReplacement(snapshot, elementID)
	if err != nil {
		return RouterReplacement{}, err
	}
	generation, err := e.store.Write(ctx, digest, e.now, basis, snapshot.Editable)
	if err != nil {
		return RouterReplacement{}, err
	}
	result.Basis.DraftGeneration = generation
	return result, nil
}

func readRouterReplacement(snapshot RouterDraftSnapshot, elementID string) (RouterReplacement, error) {
	selection, err := routerdraft.InspectReplacement(snapshot, elementID)
	if err != nil {
		return RouterReplacement{}, err
	}
	return RouterReplacement{Basis: snapshot.Basis, Name: snapshot.Name, ReplacementSelection: selection}, nil
}
