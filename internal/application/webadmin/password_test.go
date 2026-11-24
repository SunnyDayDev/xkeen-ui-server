package webadmin

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestPasswordVerificationValues(t *testing.T) {
	password := []byte("  пример-длинного-пароля ")
	first, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !VerifyPassword(first, password) || !VerifyPassword(second, password) {
		t.Fatal("expected independent verification values for the same password")
	}
	if VerifyPassword(first, bytes.TrimSpace(password)) || VerifyPassword(first, []byte("another long password")) {
		t.Fatal("accepted a different password")
	}
	first.Version++
	if VerifyPassword(first, password) {
		t.Fatal("accepted an unknown verifier version")
	}
}

func TestPasswordSetupRejectsInvalidInput(t *testing.T) {
	for _, secret := range [][]byte{nil, []byte("четырнадцать"), []byte(strings.Repeat("x", 1025)), append([]byte(strings.Repeat("x", 15)), 0xff)} {
		if _, err := HashPassword(secret); !errors.Is(err, ErrInvalidPassword) {
			t.Fatalf("invalid input of %d bytes: %v", len(secret), err)
		}
	}
}
