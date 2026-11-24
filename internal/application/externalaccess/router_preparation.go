package externalaccess

import (
	"context"
	"errors"
	"strconv"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerpreparation"
)

var ErrPreparationMissing = errors.New("router preparation source missing")

type RouterPreparationStore interface {
	ReadExternalPreparation(context.Context, Permit, string, string) (routerpreparation.Source, bool, error)
	ForExternalPreparation(Permit) publication.Store
}

type RouterPreparation struct {
	store     RouterPreparationStore
	validator publication.CatalogValidator
}

func NewRouterPreparation(store RouterPreparationStore, validator publication.CatalogValidator) RouterPreparation {
	return RouterPreparation{store: store, validator: validator}
}

type PreparationField struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}
type PreparationElement struct {
	Section   string             `json:"section"`
	ElementID string             `json:"elementId"`
	Inactive  bool               `json:"inactive,omitempty"`
	Variables []PreparationField `json:"variables"`
}
type PreparationView struct {
	RouterID               string                       `json:"routerId"`
	TemplateID             string                       `json:"templateId"`
	Source                 routerpreparation.SourceKind `json:"source,omitempty"`
	Version                string                       `json:"version,omitempty"`
	InitialDesiredRevision string                       `json:"initialDesiredRevision,omitempty"`
	Elements               []PreparationElement         `json:"elements,omitempty"`
}
type Preparation struct {
	View   PreparationView
	source routerpreparation.Source
}
type PreparationReceipt struct {
	RouterID        string `json:"routerId"`
	TemplateID      string `json:"templateId"`
	DesiredRevision string `json:"desiredRevision"`
}

func (s RouterPreparation) Inspect(ctx context.Context, permit Permit, router, template string) (Preparation, error) {
	source, prepared, err := s.store.ReadExternalPreparation(ctx, permit, router, template)
	if err != nil {
		return Preparation{}, err
	}
	view := PreparationView{RouterID: router, TemplateID: template}
	if prepared {
		view.InitialDesiredRevision = "1"
		return Preparation{View: view}, nil
	}
	fields, err := routerpreparation.Describe(source.Snapshot)
	if err != nil {
		return Preparation{}, err
	}
	view.Source = source.Kind
	view.Version = strconv.FormatInt(source.Snapshot.Version, 10)
	for _, field := range fields {
		group := PreparationElement{Section: field.Section, ElementID: field.ElementID, Inactive: field.Inactive}
		for _, variable := range field.Variables {
			group.Variables = append(group.Variables, PreparationField{Name: variable.Name, Type: variable.Type, Required: variable.Required})
		}
		view.Elements = append(view.Elements, group)
	}
	return Preparation{View: view, source: source}, nil
}

func (s RouterPreparation) Prepare(ctx context.Context, permit Permit, before Preparation, name string, personal []routerpreparation.PersonalInput) (PreparationReceipt, error) {
	if before.source.Snapshot == nil {
		return PreparationReceipt{}, publication.ErrConflict
	}
	input, err := routerpreparation.BuildPublication(before.source, before.View.RouterID, name, personal)
	if err != nil {
		return PreparationReceipt{}, err
	}
	result, err := publication.NewService(s.store.ForExternalPreparation(permit), s.validator).Publish(ctx, input)
	if errors.Is(err, publication.ErrEffectRefused) {
		_, _, admission := s.store.ReadExternalPreparation(ctx, permit, before.View.RouterID, before.View.TemplateID)
		if errors.Is(admission, ErrDisabled) || errors.Is(admission, ErrUnauthorized) {
			return PreparationReceipt{}, admission
		}
		return PreparationReceipt{}, ErrUnauthorized
	}
	if err != nil {
		return PreparationReceipt{}, err
	}
	return PreparationReceipt{RouterID: before.View.RouterID, TemplateID: before.View.TemplateID, DesiredRevision: strconv.FormatInt(result.Revision, 10)}, nil
}
