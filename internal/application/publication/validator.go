// Package publication coordinates preparation and acceptance of desired catalogs.
package publication

import (
 "context"
 "errors"

 "github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

var (
 ErrCatalogRejected = errors.New("catalog rejected")
 ErrValidatorUnavailable = errors.New("validator unavailable")
)

// CatalogValidator checks the exact profile and named bytes owned by its caller.
type CatalogValidator interface {
 Validate(context.Context, string, []configuration.CatalogFile) error
}
