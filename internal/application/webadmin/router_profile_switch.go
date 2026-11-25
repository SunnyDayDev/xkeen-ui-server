package webadmin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

type ProfileSwitchStore interface {
	ForProfileSwitch([32]byte, func() time.Time, time.Time) publication.ProfileSwitchStore
}

type RouterProfileSwitch struct {
	store     ProfileSwitchStore
	validator publication.CatalogValidator
	clock     func() time.Time
	receipts  publication.ProfileSwitchReceipts
}

func NewRouterProfileSwitch(store ProfileSwitchStore, validator publication.CatalogValidator, clock func() time.Time) *RouterProfileSwitch {
	return &RouterProfileSwitch{store: store, validator: validator, clock: clock, receipts: publication.NewProfileSwitchReceipts()}
}

type ProfileVariable struct {
	ElementID string `json:"elementId"`
	Section   string `json:"section"`
	routerdraft.VariableValue
}

type ProfileTarget struct {
	ActiveTemplateID string            `json:"activeTemplateId"`
	TargetTemplateID string            `json:"targetTemplateId"`
	Restoring        bool              `json:"restoring"`
	Fields           []ProfileVariable `json:"fields"`
}

type WebProfilePreview struct {
	Receipt   string                          `json:"receipt"`
	Changed   bool                            `json:"changed"`
	Restoring bool                            `json:"restoring"`
	Changes   []publication.CatalogFileChange `json:"changes"`
	Order     publication.ProfileOrder        `json:"order"`
}

func (s *RouterProfileSwitch) Read(ctx context.Context, token, routerID, targetID string) (ProfileTarget, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return ProfileTarget{}, err
	}
	in, err := s.service(digest, time.Time{}).Read(ctx, routerID, targetID)
	if err != nil {
		return ProfileTarget{}, profileWebError(err)
	}
	if in.AlreadyActive {
		return ProfileTarget{}, publication.ErrProfileAlreadyActive
	}
	draft := routersettings.Draft{ID: routerID, TemplateID: targetID}
	if in.TargetDraft != nil {
		draft = in.TargetDraft.Draft
	}
	snapshot := routerdraft.Snapshot{Basis: routerdraft.Basis{RouterID: routerID, TemplateID: targetID}, Accepted: in.Target, Editable: draft}
	out := ProfileTarget{ActiveTemplateID: in.Basis.SourceTemplateID, TargetTemplateID: targetID, Restoring: in.TargetDraft != nil, Fields: []ProfileVariable{}}
	for _, section := range in.Target.Sections {
		for _, element := range section.Elements {
			if element.Disabled && publication.SupportsTemplateDisabling(section.Name) || profileElementInactive(draft, section.Name, element.Element.ID) {
				continue
			}
			selection, err := routerdraft.InspectVariables(snapshot, element.Element.ID)
			if err != nil {
				return ProfileTarget{}, publication.ErrStoredState
			}
			for _, row := range selection.Definitions {
				out.Fields = append(out.Fields, ProfileVariable{ElementID: element.Element.ID, Section: section.Name, VariableValue: row})
			}
		}
	}
	return out, nil
}

func profileElementInactive(draft routersettings.Draft, section, id string) bool {
	if section == "rules" && slices.Contains(draft.DisabledRules, id) || section == "inbounds" && slices.Contains(draft.DisabledInbounds, id) {
		return true
	}
	for _, group := range draft.Groups {
		if group.Name != section {
			continue
		}
		for _, source := range group.Exclusions {
			if source.TemplateID == draft.TemplateID && source.ElementID == id {
				return true
			}
		}
	}
	return false
}

func (s *RouterProfileSwitch) Preview(ctx context.Context, token, csrf string, request publication.ProfileSwitchRequest) (WebProfilePreview, error) {
	digest, _, err := SessionProof(token, csrf, true)
	if err != nil {
		return WebProfilePreview{}, err
	}
	service := s.service(digest, time.Time{})
	preview, err := service.Preview(ctx, request)
	if err != nil {
		return WebProfilePreview{}, profileWebError(err)
	}
	in, err := service.Read(ctx, request.RouterID, request.TargetTemplateID)
	if err != nil {
		return WebProfilePreview{}, profileWebError(err)
	}
	if in.Basis != preview.Proof.Basis || in.AlreadyActive {
		return WebProfilePreview{}, publication.ErrConflict
	}
	return WebProfilePreview{
		Receipt: s.receipts.Issue(digest, profileBasisHash(in.Basis), preview.Proof.Fingerprint, s.clock()),
		Changed: preview.Changed, Restoring: preview.Restoring, Changes: append([]publication.CatalogFileChange{}, preview.Changes...),
		Order: preview.Order,
	}, nil
}

func (s *RouterProfileSwitch) Confirm(ctx context.Context, token, csrf string, request publication.ProfileSwitchRequest, receipt string) (*publication.PublishResult, error) {
	digest, _, err := SessionProof(token, csrf, true)
	if err != nil {
		return nil, err
	}
	claim, err := s.receipts.Open(digest, receipt, s.clock())
	if err != nil {
		return nil, err
	}
	service := s.service(digest, claim.Expires)
	in, err := service.Read(ctx, request.RouterID, request.TargetTemplateID)
	if err != nil {
		return nil, profileWebError(err)
	}
	if in.AlreadyActive || profileBasisHash(in.Basis) != claim.Basis {
		return nil, publication.ErrConflict
	}
	result, err := service.Confirm(ctx, request, publication.ProfileSwitchProof{Basis: in.Basis, Fingerprint: claim.Fingerprint})
	return result, profileWebError(err)
}

func (s *RouterProfileSwitch) service(digest [32]byte, expires time.Time) *publication.ProfileSwitchService {
	return publication.NewProfileSwitchService(s.store.ForProfileSwitch(digest, s.clock, expires), s.validator)
}

func profileWebError(err error) error {
	if errors.Is(err, publication.ErrEffectRefused) {
		return ErrSignedOut
	}
	return err
}

func profileBasisHash(basis publication.ProfileSwitchBasis) [32]byte {
	raw, _ := json.Marshal(basis)
	return sha256.Sum256(raw)
}
