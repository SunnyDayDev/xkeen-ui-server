//go:build linux

package localvalidation

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type fakeObserver struct{}

func (fakeObserver) Observe(context.Context) (preparation.Baseline, error) {
	return preparation.Baseline{Runtime: preparation.Runtime{PID: 4, StartTick: 5, VerifierID: "lab-v1"}}, nil
}

type fakeRunner struct{ called bool }

func (f *fakeRunner) Run(_ context.Context, id string, input agentrevision.Snapshot) error {
	f.called = id == "one.example" && input.Number == 8
	return preparation.ErrRejected
}

func TestProtectedLocalProtocolSeparatesScopeAndReturnsSafeVerdicts(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("local supervisor uses root-owned sockets")
	}
	scope := preparation.Scope{Origin: "https://control.example", RouterID: "one.example", Profile: configuration.XrayIdentityVersion, VerifierID: "lab-v1"}
	socket := filepath.Join(t.TempDir(), "local.sock")
	listener, err := Listen(socket, uint32(os.Geteuid()), uint32(os.Getegid()))
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	srv := &http.Server{Handler: Handler(scope, fakeObserver{}, runner), ReadHeaderTimeout: jobDeadline}
	go srv.Serve(listener)
	defer srv.Close()
	client, err := NewClient(socket, scope)
	if err != nil {
		t.Fatal(err)
	}
	base, err := client.Observe(t.Context())
	if err != nil || base.Runtime.VerifierID != scope.VerifierID {
		t.Fatalf("observe=%v, %v", base, err)
	}
	candidate := agentrevision.Snapshot{Number: 8, Profile: scope.Profile}
	for _, name := range []string{"01_log.json", "02_api.json", "03_inbounds.json", "04_outbounds.json", "05_routing.json"} {
		candidate.Files = append(candidate.Files, configuration.CatalogFile{Name: name, Content: []byte("{}")})
	}
	foreign := scope
	foreign.Origin = "https://another.example"
	if err := client.Check(t.Context(), foreign, candidate); !errors.Is(err, preparation.ErrUnavailable) || runner.called {
		t.Fatalf("foreign scope=%v", err)
	}
	if err := client.Check(t.Context(), scope, candidate); !errors.Is(err, preparation.ErrRejected) || !runner.called {
		t.Fatalf("local rejection=%v", err)
	}
}
