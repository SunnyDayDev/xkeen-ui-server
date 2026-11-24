package webadmin

import (
	"context"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
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
	Basis       RouterDraftBasis
	Name        string
	ElementID   string
	Section     string
	Mode        configuration.ContentMode
	HasOverride bool
	Source      *string
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
	if snapshot.Editable.TemplateID != basis.TemplateID {
		return RouterReplacement{}, ErrRouterDraftForeign
	}
	_, section, err := sharedRouterElement(snapshot, elementID)
	if err != nil {
		return RouterReplacement{}, err
	}
	source := configuration.ElementSource{TemplateID: basis.TemplateID, ElementID: elementID}
	snapshot.Editable, err = routersettings.EditReplacement(snapshot.Editable, section, source, action)
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
	if snapshot.Editable.TemplateID != snapshot.Basis.TemplateID {
		return RouterReplacement{}, ErrRouterDraftForeign
	}
	_, section, err := sharedRouterElement(snapshot, elementID)
	if err != nil {
		return RouterReplacement{}, err
	}
	source := configuration.ElementSource{TemplateID: snapshot.Basis.TemplateID, ElementID: elementID}
	override, err := routersettings.SharedOverride(snapshot.Editable, section, source)
	if err != nil {
		return RouterReplacement{}, err
	}
	result := RouterReplacement{Basis: snapshot.Basis, Name: snapshot.Name, Section: section,
		ElementID: elementID, Mode: configuration.ContentModeVariables, HasOverride: override != nil}
	if override != nil {
		result.Mode = override.Mode
		if override.Mode == configuration.ContentModeReplacement {
			content := string(override.Content)
			result.Source = &content
		}
	}
	return result, nil
}
