package publication

import (
	"crypto/sha256"
	"strings"
	"testing"
	"time"
)

func TestProfileSwitchReceiptBindsAuthorityAndExactExpiry(t *testing.T) {
	authority := sha256.Sum256([]byte("owner-session"))
	untrusted := sha256.Sum256([]byte("other-session"))
	basis := sha256.Sum256([]byte(strings.Repeat("exact-deployment-profile ", 10000)))
	fingerprint := sha256.Sum256([]byte("verified candidate"))
	now := time.Unix(1_750_000_000, 256)
	receipts := NewProfileSwitchReceipts()
	encoded := receipts.Issue(authority, basis, fingerprint, now)
	if len(encoded) > 160 {
		t.Fatalf("unbounded receipt size %d", len(encoded))
	}
	expires := now.Add(5 * time.Minute)
	claim, err := receipts.Open(authority, encoded, expires.Add(-time.Nanosecond))
	if err != nil || claim.Basis != basis || claim.Fingerprint != fingerprint || !claim.Expires.Equal(expires) {
		t.Fatalf("unexpected receipt: %+v, %v", claim, err)
	}
	if _, err := receipts.Open(authority, encoded, expires); err == nil {
		t.Fatal("receipt survived its exact deadline")
	}
	if _, err := receipts.Open(untrusted, encoded, now); err == nil {
		t.Fatal("receipt crossed sessions")
	}
	if _, err := NewProfileSwitchReceipts().Open(authority, encoded, now); err == nil {
		t.Fatal("receipt crossed purpose key or restart")
	}
	flipped := "X"
	if encoded[42] == 'X' {
		flipped = "Y"
	}
	for _, raw := range []string{encoded[:42] + flipped + encoded[43:], encoded + "A", encoded[:len(encoded)-1], "\n" + encoded} {
		if _, err := receipts.Open(authority, raw, now); err == nil {
			t.Fatal("noncanonical or altered receipt was accepted")
		}
	}
}
