package webadmin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/google/uuid"
)

type RouterRuleStore interface {
	Read(context.Context, [32]byte, func() time.Time, string) (RouterDraftSnapshot, error)
	WriteOrder(context.Context, [32]byte, func() time.Time, RouterDraftBasis, routersettings.Draft, *publication.OrderBasis) (int64, error)
}

type RouterRuleEditor struct {
	store RouterRuleStore
	now   func() time.Time
}

func NewRouterRuleEditor(store RouterRuleStore, now func() time.Time) RouterRuleEditor {
	return RouterRuleEditor{store: store, now: now}
}

type RouterRuleView struct {
	Basis         RouterDraftBasis
	Name          string
	Rules         []publication.DraftRuleEntry
	PersonalOrder bool
}

func (e RouterRuleEditor) Read(ctx context.Context, token, routerID string) (RouterRuleView, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return RouterRuleView{}, err
	}
	snapshot, err := e.store.Read(ctx, digest, e.now, routerID)
	if err != nil {
		return RouterRuleView{}, err
	}
	prepared, err := prepareEditableRules(snapshot)
	if err != nil {
		return RouterRuleView{}, err
	}
	return RouterRuleView{Basis: snapshot.Basis, Name: snapshot.Name, Rules: prepared.Entries, PersonalOrder: snapshot.Editable.PersonalOrder != nil}, nil
}

func (e RouterRuleEditor) Insert(ctx context.Context, token, csrf string, basis RouterDraftBasis, after string, source json.RawMessage) (RouterRuleView, error) {
	digest, snapshot, prepared, err := e.loadEditable(ctx, token, csrf, basis)
	if err != nil {
		return RouterRuleView{}, err
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return RouterRuleView{}, err
	}
	draft, orderBasis, err := prepared.Insert(id.String(), after, source)
	if err != nil {
		return RouterRuleView{}, err
	}
	return e.saveEditable(ctx, digest, snapshot, draft, orderBasis)
}

func (e RouterRuleEditor) Reorder(ctx context.Context, token, csrf string, basis RouterDraftBasis, ruleID string, position *int) (RouterRuleView, error) {
	digest, snapshot, prepared, err := e.loadEditable(ctx, token, csrf, basis)
	if err != nil {
		return RouterRuleView{}, err
	}
	draft, orderBasis, err := prepared.Reorder(ruleID, position)
	if err != nil {
		return RouterRuleView{}, err
	}
	return e.saveEditable(ctx, digest, snapshot, draft, orderBasis)
}

func (e RouterRuleEditor) loadEditable(ctx context.Context, token, csrf string, basis RouterDraftBasis) ([32]byte, RouterDraftSnapshot, *publication.DraftRules, error) {
	digest, _, err := SessionProof(token, csrf, true)
	if err != nil {
		return digest, RouterDraftSnapshot{}, nil, err
	}
	snapshot, err := e.store.Read(ctx, digest, e.now, basis.RouterID)
	if err != nil {
		return digest, RouterDraftSnapshot{}, nil, err
	}
	if snapshot.Basis != basis {
		return digest, RouterDraftSnapshot{}, nil, ErrRouterDraftConflict
	}
	prepared, err := prepareEditableRules(snapshot)
	return digest, snapshot, prepared, err
}

func (e RouterRuleEditor) saveEditable(ctx context.Context, digest [32]byte, snapshot RouterDraftSnapshot, draft routersettings.Draft, orderBasis *publication.OrderBasis) (RouterRuleView, error) {
	basis := snapshot.Basis
	result, err := publication.PrepareDraftRules(snapshot.Accepted, draft, orderBasis)
	if err != nil {
		return RouterRuleView{}, err
	}
	generation, err := e.store.WriteOrder(ctx, digest, e.now, basis, draft, orderBasis)
	if err != nil {
		return RouterRuleView{}, err
	}
	basis.DraftGeneration = generation
	return RouterRuleView{Basis: basis, Name: snapshot.Name, Rules: result.Entries, PersonalOrder: draft.PersonalOrder != nil}, nil
}

func prepareEditableRules(snapshot RouterDraftSnapshot) (*publication.DraftRules, error) {
	if snapshot.Editable.TemplateID != snapshot.Basis.TemplateID {
		return nil, ErrRouterDraftForeign
	}
	return publication.PrepareDraftRules(snapshot.Accepted, snapshot.Editable, snapshot.OrderBasis)
}
