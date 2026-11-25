package externalaccess

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
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

type ProfileSwitchField struct {
	ElementID        string `json:"elementId"`
	Name             string `json:"name"`
	Type             string `json:"type"`
	Required         bool   `json:"required"`
	HasPersonalValue bool   `json:"hasPersonalValue"`
}

// ProfileSwitchFields reports active variable definitions without copying their saved values into the response.
func ProfileSwitchFields(current *publication.ProfileSwitchInputs) ([]ProfileSwitchField, error) {
	fields := []ProfileSwitchField{}
	if current.AlreadyActive {
		return fields, nil
	}
	draft := routersettings.Draft{ID: current.Basis.RouterID, TemplateID: current.Basis.TargetTemplateID}
	if current.TargetDraft != nil {
		draft = current.TargetDraft.Draft
	}
	skipped := map[string]bool{}
	for _, id := range draft.DisabledRules {
		skipped[id] = true
	}
	for _, id := range draft.DisabledInbounds {
		skipped[id] = true
	}
	for _, g := range draft.Groups {
		for _, source := range g.Exclusions {
			if source.TemplateID == draft.TemplateID {
				skipped[source.ElementID] = true
			}
		}
	}
	for _, section := range current.Target.Sections {
		for _, item := range section.Elements {
			e := item.Element
			if skipped[e.ID] || item.Disabled || len(e.Variables) == 0 {
				continue
			}
			o, err := routersettings.SharedOverride(draft, section.Name, configuration.ElementSource{TemplateID: draft.TemplateID, ElementID: e.ID})
			if err != nil {
				return nil, publication.ErrInvalidCandidate
			}
			if o != nil && o.Mode == configuration.ContentModeReplacement {
				continue
			}
			inspected := configuration.InspectVariableDefinitions(e.ID, e.Variables)
			for i, v := range e.Variables {
				personal := false
				if o != nil {
					_, personal = o.Values[v.Name]
				}
				fields = append(fields, ProfileSwitchField{ElementID: e.ID, Name: v.Name, Type: v.Type, Required: inspected[i].Required, HasPersonalValue: personal})
			}
		}
	}
	return fields, nil
}
