package restart

import (
	"context"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/desired"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

// CentralClient reads identity and desired data at one pinned HTTPS origin.
type CentralClient interface {
	Origin() string
	Identity(context.Context, string) (string, error)
	ReadDesired(context.Context, string, string) (desired.Response, error)
}

type CentralSource struct {
	scope  preparation.Scope
	client CentralClient
}

func NewCentralSource(scope preparation.Scope, client CentralClient) CentralSource {
	return CentralSource{scope: scope, client: client}
}

func (s CentralSource) ReadCurrent(ctx context.Context, key string) (Desired, error) {
	if s.client.Origin() != s.scope.Origin {
		return Desired{}, ErrDesired
	}
	router, err := s.client.Identity(ctx, key)
	if err != nil || router != s.scope.RouterID {
		return Desired{}, ErrDesired
	}
	response, err := s.client.ReadDesired(ctx, key, "")
	if err != nil || response.Kind != desired.Complete {
		return Desired{}, ErrDesired
	}
	snapshot, err := agentrevision.Decode(router, response.Tag, response.Body)
	if err != nil || snapshot.Profile != s.scope.Profile {
		return Desired{}, ErrDesired
	}
	return Desired{Tag: response.Tag, Snapshot: *snapshot}, nil
}
