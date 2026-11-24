package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
)

type dependencyRead func(context.Context, externalaccess.Permit, string) (routerdraft.Snapshot, error)

func (f dependencyRead) ReadExternalRouterDraft(c context.Context, p externalaccess.Permit, id string) (routerdraft.Snapshot, error) {
	return f(c, p, id)
}

func TestExternalDependenciesRecheckBeforeDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{{"revoked", externalaccess.ErrUnauthorized, 401}, {"hidden", routerdraft.ErrMissing, 404}, {"changed", nil, 409}} {
		t.Run(test.name, func(t *testing.T) {
			reads := 0
			store := dependencyRead(func(context.Context, externalaccess.Permit, string) (routerdraft.Snapshot, error) {
				reads++
				snap := routerdraft.Snapshot{Basis: routerdraft.Basis{RouterID: "example", DraftGeneration: int64(reads)}}
				if reads == 2 {
					return snap, test.err
				}
				return snap, nil
			})
			w := httptest.NewRecorder()
			NewExternalRouterDependencies(store, nil)(w, httptest.NewRequest("GET", "https://console.example.net/api/v1/routers/dependencies?routerId=example", nil), externalaccess.Permit{})
			if reads != 2 || w.Code != test.status || strings.Contains(w.Body.String(), "references") || strings.Contains(w.Body.String(), "issues") {
				t.Fatal(reads, w.Code, w.Body.String())
			}
		})
	}
}
