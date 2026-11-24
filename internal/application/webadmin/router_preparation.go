package webadmin

import (
	"context"
	"errors"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerpreparation"
	"github.com/google/uuid"
)

var ErrInvalidPreparation = routerpreparation.ErrInvalidPreparation

type PreparationInputError = routerpreparation.PreparationInputError
type PreparationElement = routerpreparation.PreparationElement
type PreparationVariable = routerpreparation.PreparationVariable
type PersonalInput = routerpreparation.PersonalInput

var ErrPreparationSourceMissing = errors.New("router preparation source missing")

type PreparationSourceKind = routerpreparation.SourceKind
type PreparationSource = routerpreparation.Source

const (
	PreparationAccepted = routerpreparation.Accepted
	PreparationDraft    = routerpreparation.Draft
)

type PreparationForm struct {
	RouterID, TemplateID string
	Kind                 PreparationSourceKind
	Version              int64
	Elements             []PreparationElement
}

type PreparationConfirmation struct {
	RouterID, Name, TemplateID string
	Kind                       PreparationSourceKind
	Version                    int64
	Values                     []PersonalInput
}

type PreparedRouter struct {
	RouterID, Name, TemplateID string
	Revision                   int64
}

var ErrPreparedRouterMissing = errors.New("prepared router missing")

type RouterPreparationStore interface {
	ReadSource(context.Context, [32]byte, func() time.Time, string) (PreparationSource, error)
	ReadPrepared(context.Context, [32]byte, func() time.Time, string) (PreparedRouter, error)
	ForPublication([32]byte, func() time.Time) publication.Store
}

type RouterPreparation struct {
	store     RouterPreparationStore
	validator publication.CatalogValidator
	now       func() time.Time
}

func NewRouterPreparation(store RouterPreparationStore, validator publication.CatalogValidator, now func() time.Time) RouterPreparation {
	return RouterPreparation{store: store, validator: validator, now: now}
}

func (s RouterPreparation) Confirm(ctx context.Context, token, proof string, in PreparationConfirmation) (PreparedRouter, error) {
	digest, _, err := SessionProof(token, proof, true)
	if err != nil {
		return PreparedRouter{}, err
	}
	source, err := s.store.ReadSource(ctx, digest, s.now, in.TemplateID)
	if err != nil {
		return PreparedRouter{}, err
	}
	if source.Kind != in.Kind || source.Snapshot.ID != in.TemplateID || source.Snapshot.Version != in.Version {
		return PreparedRouter{}, publication.ErrConflict
	}
	request, err := routerpreparation.BuildPublication(source, in.RouterID, in.Name, in.Values)
	if err != nil {
		return PreparedRouter{}, err
	}
	published, err := publication.NewService(s.store.ForPublication(digest, s.now), s.validator).Publish(ctx, request)
	if errors.Is(err, publication.ErrEffectRefused) {
		return PreparedRouter{}, ErrSignedOut
	}
	if err != nil {
		return PreparedRouter{}, err
	}
	return PreparedRouter{RouterID: in.RouterID, Name: in.Name, TemplateID: in.TemplateID, Revision: published.Revision}, nil
}

func (s RouterPreparation) Prepared(ctx context.Context, token, id string) (PreparedRouter, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return PreparedRouter{}, err
	}
	return s.store.ReadPrepared(ctx, digest, s.now, id)
}

func (s RouterPreparation) Describe(ctx context.Context, token, templateID string) (PreparationForm, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return PreparationForm{}, err
	}
	source, err := s.store.ReadSource(ctx, digest, s.now, templateID)
	if err != nil {
		return PreparationForm{}, err
	}
	fields, err := routerpreparation.Describe(source.Snapshot)
	if err != nil {
		return PreparationForm{}, err
	}
	id, err := newPreparationID()
	if err != nil {
		return PreparationForm{}, err
	}
	return PreparationForm{
		RouterID: id, TemplateID: source.Snapshot.ID, Kind: source.Kind,
		Version: source.Snapshot.Version, Elements: fields,
	}, nil
}

func newPreparationID() (string, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
