package webadmin

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const MaxPasswordBytes = 1024

var ErrInvalidPassword = errors.New("password must be valid UTF-8 with at least 15 characters and at most 1024 bytes")

// Verifier holds the versioned KDF parameters by reference to their fixed version.
type Verifier struct {
	Version int
	Salt    [16]byte
	Digest  [32]byte
}

func HashPassword(password []byte) (Verifier, error) {
	if !validPassword(password) {
		return Verifier{}, ErrInvalidPassword
	}
	verifier := Verifier{Version: 1}
	if _, err := rand.Read(verifier.Salt[:]); err != nil {
		return Verifier{}, err
	}
	copy(verifier.Digest[:], passwordKey(password, verifier.Salt))
	return verifier, nil
}

func VerifyPassword(verifier Verifier, password []byte) bool {
	return verifier.Version == 1 && validPassword(password) &&
		subtle.ConstantTimeCompare(passwordKey(password, verifier.Salt), verifier.Digest[:]) == 1
}

func validPassword(password []byte) bool {
	return len(password) <= MaxPasswordBytes && utf8.Valid(password) && utf8.RuneCount(password) >= 15
}

func passwordKey(password []byte, salt [16]byte) []byte {
	return argon2.IDKey(password, salt[:], 3, 64*1024, 4, 32)
}
