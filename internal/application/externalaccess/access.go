// Package externalaccess defines the independent authority for management API clients.
package externalaccess

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
)

var (
	ErrDisabled     = errors.New("external management API unavailable")
	ErrUnauthorized = errors.New("external management credentials required")
	ErrConflict     = errors.New("external management settings changed")
)

type Settings struct {
	Enabled   bool  `json:"enabled"`
	Protected bool  `json:"protected"`
	HasToken  bool  `json:"hasToken"`
	Version   int64 `json:"version"`
}

type Change struct {
	Version            int64
	Enabled, Protected bool
	Rotate             bool
}

// Permit represents one authenticated generation. The operation's transaction
// rechecks this generation before reading or committing protected resources.
type Permit struct{ Version int64 }

func Issue() (string, [32]byte, error) {
	var raw [32]byte
	_, err := rand.Read(raw[:])
	if err != nil {
		return "", [32]byte{}, err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), sha256.Sum256(raw[:]), nil
}

func Admit(s Settings, digest []byte, headers []string) (Permit, error) {
	if !s.Enabled {
		return Permit{}, ErrDisabled
	}
	if s.Protected {
		if len(headers) != 1 || !s.HasToken {
			return Permit{}, ErrUnauthorized
		}
		scheme, token, ok := strings.Cut(headers[0], " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || len(token) != 43 {
			return Permit{}, ErrUnauthorized
		}
		raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
		if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != token {
			return Permit{}, ErrUnauthorized
		}
		actual := sha256.Sum256(raw)
		if subtle.ConstantTimeCompare(digest, actual[:]) != 1 {
			return Permit{}, ErrUnauthorized
		}
	}
	return Permit{Version: s.Version}, nil
}
