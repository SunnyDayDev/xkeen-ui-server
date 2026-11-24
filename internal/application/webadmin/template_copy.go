package webadmin

import (
	"context"
	"time"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
)

type TemplateCopyStore interface {
	Copy(context.Context, [32]byte, func() time.Time, string, string, int64) error
}

type TemplateCopy struct {
	store TemplateCopyStore
	now   func() time.Time
}

func NewTemplateCopy(store TemplateCopyStore, now func() time.Time) TemplateCopy {
	return TemplateCopy{store: store, now: now}
}

func (s TemplateCopy) Copy(ctx context.Context, token, proof, source, target string, version int64) (TemplateSummary, error) {
	digest, _, err := SessionProof(token, proof, true)
	if err != nil {
		return TemplateSummary{}, err
	}
	if source == "" || target == "" || !utf8.ValidString(source) || !utf8.ValidString(target) || version < 1 {
		return TemplateSummary{}, templates.ErrInvalid
	}
	if err := s.store.Copy(ctx, digest, s.now, source, target, version); err != nil {
		return TemplateSummary{}, err
	}
	return TemplateSummary{ID: target, Version: 1}, nil
}
