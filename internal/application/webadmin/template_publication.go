package webadmin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

const templatePreviewLifetime = 5 * time.Minute

var ErrTemplatePreviewChanged = errors.New("publication preview has changed")
var previewEncoding = base64.RawURLEncoding.Strict()

type TemplatePublicationStore interface {
	ForTemplateGroup([32]byte, func() time.Time, time.Time) publication.TemplateGroupStore
	CheckPreview(context.Context, [32]byte, func() time.Time, *publication.TemplateGroupPreview) ([]reporting.Presence, error)
}

type TemplatePublication struct {
	store     TemplatePublicationStore
	validator publication.CatalogValidator
	now       func() time.Time
	key       [32]byte
}

type TemplatePublicationPreview struct {
	Preview *publication.TemplateGroupPreview
	Contact []reporting.Presence
	Receipt string
}

type previewReceipt struct {
	ID                string
	Version, Accepted int64
	Expires           int64
	Plan              [sha256.Size]byte
}

func NewTemplatePublication(store TemplatePublicationStore, validator publication.CatalogValidator, now func() time.Time) TemplatePublication {
	s := TemplatePublication{store: store, validator: validator, now: now}
	rand.Read(s.key[:])
	return s
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
	payload, err := json.Marshal(previewReceipt{
		ID: id, Version: preview.Version, Accepted: preview.AcceptedVersion,
		Expires: s.now().Add(templatePreviewLifetime).UnixNano(), Plan: preview.Fingerprint,
	})
	if err != nil {
		return nil, err
	}
	body := previewEncoding.EncodeToString(payload)
	receipt := body + "." + previewEncoding.EncodeToString(s.signReceipt(digest, body))
	return &TemplatePublicationPreview{Preview: preview, Contact: status, Receipt: receipt}, nil
}

func (s TemplatePublication) Confirm(ctx context.Context, token, csrf, id, receipt string) (*publication.TemplateGroupResult, error) {
	digest, _, err := SessionProof(token, csrf, true)
	if err != nil {
		return nil, err
	}
	body, signature, ok := strings.Cut(receipt, ".")
	auth, err := previewEncoding.DecodeString(signature)
	if !ok || err != nil || !hmac.Equal(auth, s.signReceipt(digest, body)) {
		return nil, ErrTemplatePreviewChanged
	}
	raw, err := previewEncoding.DecodeString(body)
	var claim previewReceipt
	if err != nil || json.Unmarshal(raw, &claim) != nil || claim.ID != id || claim.Version < 1 {
		return nil, ErrTemplatePreviewChanged
	}
	expires := time.Unix(0, claim.Expires)
	if !s.now().Before(expires) {
		return nil, ErrTemplatePreviewChanged
	}
	groups := publication.NewTemplateGroupService(s.store.ForTemplateGroup(digest, s.now, expires), s.validator)
	result, err := groups.Publish(ctx, publication.SharedPublishRequest{
		TemplateID: id, TemplateDraftVersion: claim.Version, AcceptedVersion: claim.Accepted,
		Source: publication.SourceSharedDraft, PreviewFingerprint: &claim.Plan,
	})
	if err != nil {
		return nil, templatePublicationError(err)
	}
	return result, nil
}

func (s TemplatePublication) signReceipt(session [32]byte, body string) []byte {
	mac := hmac.New(sha256.New, s.key[:])
	_, _ = mac.Write(session[:])
	_, _ = mac.Write([]byte(body))
	return mac.Sum(nil)
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
