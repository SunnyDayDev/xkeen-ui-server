package publication

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"
)

const profileSwitchReceiptLifetime = 5 * time.Minute
const profileSwitchReceiptPayload = 8 + 2*sha256.Size

var ErrProfileSwitchReceipt = errors.New("profile switch proof no longer valid")

type ProfileSwitchReceipts struct{ key [32]byte }

type ProfileSwitchReceipt struct {
	Basis       [32]byte
	Fingerprint [32]byte
	Expires     time.Time
}

func NewProfileSwitchReceipts() ProfileSwitchReceipts {
	var signer ProfileSwitchReceipts
	rand.Read(signer.key[:])
	return signer
}

// Issue seals fixed-size digests, including the basis digest instead of the full
// caller-supplied router and template identifiers.
func (s ProfileSwitchReceipts) Issue(authority, basis, fingerprint [32]byte, now time.Time) string {
	var raw [profileSwitchReceiptPayload + sha256.Size]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(now.Add(profileSwitchReceiptLifetime).UnixNano()))
	copy(raw[8:8+sha256.Size], basis[:])
	copy(raw[8+sha256.Size:profileSwitchReceiptPayload], fingerprint[:])
	copy(raw[profileSwitchReceiptPayload:], s.sign(authority, raw[:profileSwitchReceiptPayload]))
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func (s ProfileSwitchReceipts) Open(authority [32]byte, encoded string, now time.Time) (ProfileSwitchReceipt, error) {
	const total = profileSwitchReceiptPayload + sha256.Size
	encoding := base64.RawURLEncoding.Strict()
	if len(encoded) != encoding.EncodedLen(total) {
		return ProfileSwitchReceipt{}, ErrProfileSwitchReceipt
	}
	raw, err := encoding.DecodeString(encoded)
	if err != nil || len(raw) != total || !hmac.Equal(raw[profileSwitchReceiptPayload:], s.sign(authority, raw[:profileSwitchReceiptPayload])) {
		return ProfileSwitchReceipt{}, ErrProfileSwitchReceipt
	}
	expires := time.Unix(0, int64(binary.BigEndian.Uint64(raw[:8])))
	if !now.Before(expires) {
		return ProfileSwitchReceipt{}, ErrProfileSwitchReceipt
	}
	return ProfileSwitchReceipt{
		Basis: [32]byte(raw[8 : 8+sha256.Size]), Fingerprint: [32]byte(raw[8+sha256.Size : profileSwitchReceiptPayload]), Expires: expires,
	}, nil
}

func (s ProfileSwitchReceipts) sign(authority [32]byte, payload []byte) []byte {
	mac := hmac.New(sha256.New, s.key[:])
	mac.Write(authority[:])
	mac.Write(payload)
	return mac.Sum(nil)
}
