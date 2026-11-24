package statefile

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/desired"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestPreparedPackageReopensWithExactCandidateAndBaseline(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent")
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	catalog := agentrevision.Snapshot{Number: 8, Profile: configuration.XrayIdentityVersion}
	for _, name := range []string{"01_log.json", "02_api.json", "03_inbounds.json", "04_outbounds.json", "05_routing.json"} {
		catalog.Files = append(catalog.Files, configuration.CatalogFile{Name: name, Content: []byte(" { \"log\": \"<x>\" }\n")})
	}
	tag, body, err := agentrevision.Encode("one.example", catalog)
	if err != nil {
		t.Fatal(err)
	}
	scope := preparation.Scope{Origin: "https://server.example", RouterID: "one.example", Profile: catalog.Profile, VerifierID: "local-profile"}
	entry := &desired.Entry{Origin: scope.Origin, RouterID: scope.RouterID, Tag: tag, Body: body}
	base := preparation.Baseline{
		Runtime: preparation.Runtime{PID: 4, StartTick: 5, VerifierID: scope.VerifierID},
		Files:   []preparation.BaselineFile{{Name: "05_routing.json", Content: []byte(" {}\n"), UID: 7, GID: 8, Mode: 0640}},
	}
	before, err := preparation.New(preparedObserver{base}, store, preparedValidator{}).Prepare(t.Context(), scope, entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, err := reopened.LoadPrepared(); err != nil || !reflect.DeepEqual(got, before) {
		t.Fatalf("reopened package changed raw bytes: %v", err)
	}
	if id, err := reopened.BaselineID(); err != nil || id != before.BaselineID {
		t.Fatalf("current baseline = %q, %v", id, err)
	}
	if info, err := os.Stat(filepath.Join(dir, preparedName)); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("prepared artifact mode = %v, %v", info, err)
	}
}

type preparedObserver struct{ base preparation.Baseline }

func (o preparedObserver) Observe(context.Context) (preparation.Baseline, error) { return o.base, nil }

type preparedValidator struct{}

func (preparedValidator) Check(context.Context, preparation.Scope, agentrevision.Snapshot) error {
	return nil
}
