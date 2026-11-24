package webadmin

import (
	"context"
	"errors"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

var ErrTemplatePreviewChanged = publication.ErrGroupPreviewChanged

type TemplatePublicationStore interface {
	ForTemplateGroup([32]byte, func() time.Time, time.Time) publication.TemplateGroupStore
	CheckPreview(context.Context, [32]byte, func() time.Time, *publication.TemplateGroupPreview) ([]reporting.Presence, error)
}

type TemplatePublication struct {
	store     TemplatePublicationStore
	validator publication.CatalogValidator
	now       func() time.Time
	receipts  publication.GroupReceipts
}

type TemplatePublicationPreview struct {
	Preview *publication.TemplateGroupPreview
	Contact []reporting.Presence
	Receipt string
}

func NewTemplatePublication(store TemplatePublicationStore, validator publication.CatalogValidator, now func() time.Time) TemplatePublication {
	return TemplatePublication{store: store, validator: validator, now: now, receipts: publication.NewGroupReceipts()}
}

func (s TemplatePublication) Preview(ctx context.Context, token, id string, version int64) (*TemplatePublicationPreview, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return nil, err
	}
	groups := publication.NewTemplateGroupService(s.store.ForTemplateGroup(digest, s.now, time.Time{}), s.validator)
	preview, err := groups.Preview(ctx, id, version)
	if err != nil {
		return nil, templatePublicationError(err)
	}
	status, err := s.store.CheckPreview(ctx, digest, s.now, preview)
	if err != nil {
		return nil, templatePublicationError(err)
	}
	receipt, err := s.receipts.Issue(digest, preview, s.now())
	if err != nil {
		return nil, err
	}
	return &TemplatePublicationPreview{Preview: preview, Contact: status, Receipt: receipt}, nil
}

func (s TemplatePublication) Confirm(ctx context.Context, token, csrf, id, receipt string) (*publication.TemplateGroupResult, error) {
	digest, _, err := SessionProof(token, csrf, true)
	if err != nil {
		return nil, err
	}
	request, expires, err := s.receipts.Open(digest, id, receipt, s.now())
	if err != nil {
		return nil, err
	}
	groups := publication.NewTemplateGroupService(s.store.ForTemplateGroup(digest, s.now, expires), s.validator)
	result, err := groups.Publish(ctx, request)
	if err != nil {
		return nil, templatePublicationError(err)
	}
	return result, nil
}

func templatePublicationError(err error) error {
	switch {
	case errors.Is(err, publication.ErrEffectRefused):
		return ErrSignedOut
	case errors.Is(err, publication.ErrConflict):
		return ErrTemplatePreviewChanged
	default:
		return err
	}
}

type TemplatePublicationCurrent struct {
	TemplateID               string
	Version, AcceptedVersion int64
	Routers                  []TemplatePublicationRouter
}

type TemplatePublicationRouter struct {
	RouterID string
	Name     string
	Desired  int64
}

// Current reports the latest state after an uncertain response. It neither
// identifies the writer of that state nor repeats the requested publication.
func (s TemplatePublication) Current(ctx context.Context, token, id string) (*TemplatePublicationCurrent, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return nil, err
	}
	snapshot, err := s.store.ForTemplateGroup(digest, s.now, time.Time{}).ReadTemplateGroup(ctx, id)
	if err != nil {
		return nil, templatePublicationError(err)
	}
	if snapshot == nil || snapshot.TemplateDraft == nil || snapshot.TemplateDraft.ID != id {
		return nil, publication.ErrStoredState
	}
	current := &TemplatePublicationCurrent{TemplateID: id, Version: snapshot.TemplateDraft.Version,
		Routers: make([]TemplatePublicationRouter, 0, len(snapshot.Members))}
	if snapshot.Accepted != nil {
		current.AcceptedVersion = snapshot.Accepted.Version
	}
	for _, member := range snapshot.Members {
		name := member.Active.Draft.Name
		if name == "" {
			name = member.Active.Draft.ID
		}
		current.Routers = append(current.Routers, TemplatePublicationRouter{
			RouterID: member.Active.Draft.ID, Name: name, Desired: member.Desired.Number,
		})
	}
	return current, nil
}
