package routersettings

import (
	"context"
	"errors"
	"unicode/utf8"
)

var (
	ErrVisibilityInvalid     = errors.New("invalid router identity")
	ErrVisibilityNotFound    = errors.New("confirmed router not found")
	ErrVisibilityUnavailable = errors.New("router visibility unavailable")
)

type VisibilityRepository interface {
	Hide(context.Context, string) error
	VisibleIDs(context.Context) ([]string, error)
}

type Visibility struct{ repository VisibilityRepository }

func NewVisibility(repository VisibilityRepository) Visibility {
	return Visibility{repository: repository}
}

func (v Visibility) Hide(ctx context.Context, id string) error {
	if id == "" || !utf8.ValidString(id) {
		return ErrVisibilityInvalid
	}
	return v.repository.Hide(ctx, id)
}

func (v Visibility) VisibleIDs(ctx context.Context) ([]string, error) {
	return v.repository.VisibleIDs(ctx)
}
