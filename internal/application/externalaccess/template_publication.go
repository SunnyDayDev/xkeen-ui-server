package externalaccess

import (
	"context"
	"encoding/binary"
	"errors"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

type TemplatePublicationsStore interface {
	ForExternalTemplateGroup(Permit) publication.TemplateGroupStore
	CheckExternalTemplateGroupPreview(context.Context, Permit, func() time.Time, *publication.TemplateGroupPreview) ([]reporting.Presence, error)
}

type TemplatePublications struct {
	store     TemplatePublicationsStore
	validator publication.CatalogValidator
	now       func() time.Time
	receipts  publication.GroupReceipts
}

type TemplateGroupPreview struct {
	Plan    *publication.TemplateGroupPreview
	Contact []reporting.Presence
	Receipt string
}

func NewTemplatePublications(store TemplatePublicationsStore, validator publication.CatalogValidator, clock func() time.Time) TemplatePublications {
	return TemplatePublications{store: store, validator: validator, now: clock, receipts: publication.NewGroupReceipts()}
}

func (s TemplatePublications) Preview(ctx context.Context, permit Permit, id string, version int64) (*TemplateGroupPreview, error) {
	service := publication.NewTemplateGroupService(s.store.ForExternalTemplateGroup(permit), s.validator)
	plan, err := service.Preview(ctx, id, version)
	if err != nil {
		if _, check := s.store.CheckExternalTemplateGroupPreview(ctx, permit, s.now, nil); check != nil {
			return nil, check
		}
		if errors.Is(err, publication.ErrEffectRefused) {
			return nil, ErrUnauthorized
		}
		return nil, err
	}
	contact, err := s.store.CheckExternalTemplateGroupPreview(ctx, permit, s.now, plan)
	if err != nil {
		return nil, err
	}
	receipt, err := s.receipts.Issue(groupAuthority(permit), plan, s.now())
	if err != nil {
		return nil, err
	}
	return &TemplateGroupPreview{Plan: plan, Contact: contact, Receipt: receipt}, nil
}

func groupAuthority(permit Permit) [32]byte {
	var authority [32]byte
	binary.BigEndian.PutUint64(authority[:8], uint64(permit.Version))
	return authority
}
