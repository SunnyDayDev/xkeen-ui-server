package webadmin

import (
	"context"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type RouterExclusionEditor struct {
	Store RouterDraftStore
	Now   func() time.Time
}
type RouterExclusions struct {
	Basis    RouterDraftBasis
	Name     string
	Source   *configuration.ExclusionSource
	Override *routersettings.ExclusionOverride
	File     *configuration.ExclusionFile
	Issue    *configuration.ExclusionIssue
}

func (e RouterExclusionEditor) Read(ctx context.Context, token, id string) (RouterExclusions, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return RouterExclusions{}, err
	}
	snap, err := e.Store.Read(ctx, digest, e.Now, id)
	if err != nil {
		return RouterExclusions{}, err
	}
	return routerExclusions(snap)
}

func routerExclusions(snap RouterDraftSnapshot) (RouterExclusions, error) {
	if snap.Editable.TemplateID != snap.Basis.TemplateID {
		return RouterExclusions{}, ErrRouterDraftForeign
	}
	if snap.Exclusions == nil {
		return RouterExclusions{}, ErrRouterDraftMissing
	}
	result := RouterExclusions{Basis: snap.Basis, Name: snap.Name, Source: snap.Exclusions, Override: snap.Editable.Exclusion}
	result.File, result.Issue = snap.Editable.AssembleExclusions(snap.Exclusions)
	if result.Issue != nil && result.Issue.Code == "source_mismatch" {
		return RouterExclusions{}, ErrRouterDraftForeign
	}
	return result, nil
}

func (e RouterExclusionEditor) Save(ctx context.Context, token, csrf string, basis RouterDraftBasis, elementID string, edit routersettings.ExclusionEdit) (RouterExclusions, error) {
	digest, _, err := SessionProof(token, csrf, true)
	if err != nil {
		return RouterExclusions{}, err
	}
	snap, err := e.Store.Read(ctx, digest, e.Now, basis.RouterID)
	if err != nil {
		return RouterExclusions{}, err
	}
	if snap.Basis != basis {
		return RouterExclusions{}, ErrRouterDraftConflict
	}
	current, err := routerExclusions(snap)
	if err != nil {
		return RouterExclusions{}, err
	}
	if current.Source.ElementID != elementID {
		return RouterExclusions{}, ErrRouterDraftConflict
	}
	snap.Editable, err = routersettings.EditExclusion(snap.Editable, configuration.ElementSource{TemplateID: basis.TemplateID, ElementID: elementID}, edit)
	if err != nil {
		return RouterExclusions{}, err
	}
	result, err := routerExclusions(snap)
	if err != nil {
		return RouterExclusions{}, err
	}
	result.Basis.DraftGeneration, err = e.Store.Write(ctx, digest, e.Now, basis, snap.Editable)
	if err != nil {
		return RouterExclusions{}, err
	}
	return result, nil
}
