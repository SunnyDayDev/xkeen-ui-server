package webadmin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

var (
	ErrInvalidCredentials = errors.New("invalid administrator credentials")
	ErrRateLimited        = errors.New("login rate limited")
	ErrCSRF               = errors.New("invalid session CSRF proof")
)

const TokenBytes = 32

type Service struct {
	store Store
	now   func() time.Time

	mu        sync.Mutex
	verifying bool
	window    time.Time
	failed    int
}

func New(store Store, now func() time.Time) *Service {
	return &Service{store: store, now: now}
}

func (s *Service) Login(ctx context.Context, password []byte, prior string) (token, csrf string, err error) {
	if !s.beginVerification() {
		return "", "", ErrRateLimited
	}
	defer func() { s.endVerification(err) }()
	verifier, generation, err := s.store.Verifier(ctx)
	if err != nil {
		return "", "", err
	}
	if !VerifyPassword(verifier, password) {
		return "", "", ErrInvalidCredentials
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	var raw [TokenBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", err
	}
	old, ok := decodeToken(prior)
	var oldDigest [32]byte
	if ok {
		oldDigest = sha256.Sum256(old)
	}
	if err := s.store.Issue(ctx, generation, sha256.Sum256(raw[:]), oldDigest, s.now()); err != nil {
		return "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), csrfFor(raw[:]), nil
}

func (s *Service) Session(ctx context.Context, token, proof string, action SessionAction) (string, error) {
	raw, ok := decodeToken(token)
	if !ok {
		return "", ErrSignedOut
	}
	csrf := csrfFor(raw)
	if action != SessionRead && subtle.ConstantTimeCompare([]byte(csrf), []byte(proof)) != 1 {
		return "", ErrCSRF
	}
	if err := s.store.Session(ctx, sha256.Sum256(raw), s.now, action); err != nil {
		return "", err
	}
	return csrf, nil
}

func (s *Service) Configured(ctx context.Context) (bool, error) {
	_, _, err := s.store.Verifier(ctx)
	if errors.Is(err, ErrSetupRequired) {
		return false, nil
	}
	return err == nil, err
}

func (s *Service) beginVerification() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.verifying {
		return false
	}
	if !now.Before(s.window.Add(30 * time.Second)) {
		s.window, s.failed = now, 0
	}
	if s.failed >= 5 {
		return false
	}
	s.verifying = true
	return true
}

func (s *Service) endVerification(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if errors.Is(err, ErrInvalidCredentials) {
		s.failed++
	}
	s.verifying = false
}

func decodeToken(token string) ([]byte, bool) {
	encoding := base64.RawURLEncoding.Strict()
	if len(token) != encoding.EncodedLen(TokenBytes) {
		return nil, false
	}
	raw, err := encoding.DecodeString(token)
	return raw, err == nil && len(raw) == TokenBytes
}

func csrfFor(raw []byte) string {
	mac := hmac.New(sha256.New, raw)
	mac.Write([]byte("xkeen-ui-server web session csrf v1"))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
