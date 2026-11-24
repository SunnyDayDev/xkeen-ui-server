package publication

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const GroupPreviewLifetime = 5 * time.Minute

var ErrGroupPreviewChanged = errors.New("publication preview has changed")
var groupReceiptEncoding = base64.RawURLEncoding.Strict()

type GroupReceipts struct{ key [32]byte }

type groupReceiptClaim struct {
	TemplateHash      [sha256.Size]byte
	Version, Accepted int64
	Expires           int64
	Plan              [sha256.Size]byte
}

func NewGroupReceipts() GroupReceipts {
	var signer GroupReceipts
	rand.Read(signer.key[:])
	return signer
}

func (s GroupReceipts) Issue(authority [32]byte, preview *TemplateGroupPreview, now time.Time) (string, error) {
	payload, err := json.Marshal(groupReceiptClaim{
		TemplateHash: sha256.Sum256([]byte(preview.TemplateID)), Version: preview.Version,
		Accepted: preview.AcceptedVersion, Expires: now.Add(GroupPreviewLifetime).UnixNano(), Plan: preview.Fingerprint,
	})
	if err != nil {
		return "", err
	}
	body := groupReceiptEncoding.EncodeToString(payload)
	return body + "." + groupReceiptEncoding.EncodeToString(s.sign(authority, body)), nil
}

func (s GroupReceipts) Open(authority [32]byte, id, receipt string, now time.Time) (SharedPublishRequest, time.Time, error) {
	body, signature, ok := strings.Cut(receipt, ".")
	auth, err := groupReceiptEncoding.DecodeString(signature)
	if !ok || err != nil || !hmac.Equal(auth, s.sign(authority, body)) {
		return SharedPublishRequest{}, time.Time{}, ErrGroupPreviewChanged
	}
	raw, err := groupReceiptEncoding.DecodeString(body)
	var claim groupReceiptClaim
	if err != nil || json.Unmarshal(raw, &claim) != nil || claim.TemplateHash != sha256.Sum256([]byte(id)) || claim.Version < 1 {
		return SharedPublishRequest{}, time.Time{}, ErrGroupPreviewChanged
	}
	expires := time.Unix(0, claim.Expires)
	if !now.Before(expires) {
		return SharedPublishRequest{}, time.Time{}, ErrGroupPreviewChanged
	}
	return SharedPublishRequest{
		TemplateID: id, TemplateDraftVersion: claim.Version, AcceptedVersion: claim.Accepted,
		Source: SourceSharedDraft, PreviewFingerprint: &claim.Plan,
	}, expires, nil
}

func (s GroupReceipts) sign(authority [32]byte, body string) []byte {
	mac := hmac.New(sha256.New, s.key[:])
	_, _ = mac.Write(authority[:])
	_, _ = mac.Write([]byte(body))
	return mac.Sum(nil)
}
