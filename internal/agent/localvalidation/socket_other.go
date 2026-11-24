//go:build !linux

package localvalidation

import (
	"net/http"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
)

func newTransport(string) (*http.Transport, error) {
	return nil, preparation.ErrUnavailable
}
