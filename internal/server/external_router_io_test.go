package server

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
)

func TestExternalEffectiveIORechecksPermitBeforeAnswering(t *testing.T) {
	reads := 0
	store := dependencyRead(func(_ context.Context, _ externalaccess.Permit, id string) (routerdraft.Snapshot, error) {
		reads++
		if reads == 2 {
			return routerdraft.Snapshot{}, externalaccess.ErrUnauthorized
		}
		return routerdraft.Snapshot{Basis: routerdraft.Basis{RouterID: id}}, nil
	})
	w := httptest.NewRecorder()
	NewExternalRouterIO(store, nil)(w, httptest.NewRequest("GET", "https://console.example.net/api/v1/routers/io?routerId=example", nil), externalaccess.Permit{})
	if reads != 2 || w.Code != 401 {
		t.Fatalf("late revoked result: %d reads, %d %s", reads, w.Code, w.Body)
	}
}
