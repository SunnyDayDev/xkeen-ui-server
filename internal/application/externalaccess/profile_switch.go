package externalaccess

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
)

type ProfileSwitchStore interface {
	CheckExternalSwitchPermit(context.Context, Permit) error
	ForExternalSwitch(Permit, string, func() time.Time, time.Time) publication.ProfileSwitchStore
}
type ProfileSwitches struct {
	store     ProfileSwitchStore
	validator publication.CatalogValidator
	clock     func() time.Time
	receipts  publication.ProfileSwitchReceipts
}

func NewProfileSwitches(store ProfileSwitchStore, v publication.CatalogValidator, clock func() time.Time) ProfileSwitches {
	return ProfileSwitches{store, v, clock, publication.NewProfileSwitchReceipts()}
}
func (s ProfileSwitches) service(p Permit, id string, expires time.Time) *publication.ProfileSwitchService {
	return publication.NewProfileSwitchService(s.store.ForExternalSwitch(p, id, s.clock, expires), s.validator)
}
func switchBasisDigest(b publication.ProfileSwitchBasis) [32]byte {
	raw, _ := json.Marshal(b) // Struct contains only strings and integers.
	return sha256.Sum256(raw)
}
func (s ProfileSwitches) Read(ctx context.Context, p Permit, id, target string) (*publication.ProfileSwitchInputs, error) {
	current, err := s.service(p, id, time.Time{}).Read(ctx, id, target)
	return current, s.failure(ctx, p, err)
}
func (s ProfileSwitches) Preview(ctx context.Context, p Permit, req publication.ProfileSwitchRequest) (*publication.ProfileSwitchPreview, string, error) {
	preview, err := s.service(p, req.RouterID, time.Time{}).Preview(ctx, req)
	if err != nil {
		return nil, "", s.failure(ctx, p, err)
	}
	receipt := s.receipts.Issue(groupAuthority(p), switchBasisDigest(preview.Proof.Basis), preview.Proof.Fingerprint, s.clock())
	return preview, receipt, nil
}
func (s ProfileSwitches) Confirm(ctx context.Context, p Permit, req publication.ProfileSwitchRequest, encoded string) (*publication.PublishResult, error) {
	receipt, err := s.receipts.Open(groupAuthority(p), encoded, s.clock())
	if err != nil {
		return nil, publication.ErrConflict
	}
	service := s.service(p, req.RouterID, receipt.Expires)
	current, err := service.Read(ctx, req.RouterID, req.TargetTemplateID)
	if err != nil {
		return nil, s.failure(ctx, p, err)
	}
	if receipt.Basis != switchBasisDigest(current.Basis) {
		return nil, publication.ErrConflict
	}
	result, err := service.Confirm(ctx, req, publication.ProfileSwitchProof{Basis: current.Basis, Fingerprint: receipt.Fingerprint})
	if errors.Is(err, publication.ErrPublicationCommit) || errors.Is(err, publication.ErrEffectRefused) {
		if _, currentErr := service.Read(ctx, req.RouterID, req.TargetTemplateID); currentErr != nil {
			return nil, s.failure(ctx, p, currentErr)
		}
	}
	return result, s.failure(ctx, p, err)
}

func (s ProfileSwitches) failure(ctx context.Context, p Permit, err error) error {
	if errors.Is(err, publication.ErrEffectRefused) {
		if check := s.store.CheckExternalSwitchPermit(ctx, p); check != nil {
			return check
		}
		return ErrUnauthorized
	}
	return err
}
