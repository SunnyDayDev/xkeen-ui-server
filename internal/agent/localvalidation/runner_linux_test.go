//go:build linux

package localvalidation

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func validatorFixture(t *testing.T, dir string) agentrevision.Snapshot {
	t.Helper()
	catalog := agentrevision.Snapshot{Number: 8, Profile: configuration.XrayIdentityVersion}
	for _, name := range []string{"01_log.json", "02_api.json", "03_inbounds.json", "04_outbounds.json", "05_routing.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		catalog.Files = append(catalog.Files, configuration.CatalogFile{Name: name, Content: raw})
	}
	return catalog
}

func TestRunnerFailsClosedWithoutApprovedCgroupAndXray(t *testing.T) {
	runner := Runner{StageDir: t.TempDir(), CgroupDir: t.TempDir(), Binary: "/path/to/unavailable/xray"}
	catalog := agentrevision.Snapshot{Number: 8, Profile: configuration.XrayIdentityVersion}
	if err := runner.Run(t.Context(), "one.example", catalog); !errors.Is(err, preparation.ErrUnavailable) {
		t.Fatalf("unproved platform returned %v", err)
	}
}

func TestRunnerChecksRealXrayInTheJob(t *testing.T) {
	if os.Getenv("XKEEN_TEST_LOCAL_VALIDATOR") == "" {
		t.Skip("requires disposable delegated cgroup and pinned Xray")
	}
	binary := os.Getenv("XKEEN_TEST_XRAY")
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	runner := Runner{StageDir: t.TempDir(), CgroupDir: os.Getenv("XKEEN_TEST_CGROUP"), Binary: binary,
		Digest: sha256.Sum256(raw), UID: 65534, GID: 65534}
	candidate := validatorFixture(t, os.Getenv("XKEEN_TEST_CATALOG"))
	if err := runner.Run(t.Context(), "one.example", candidate); err != nil {
		t.Fatalf("valid exact local Xray catalog failed: %v", err)
	}
	candidate.Files[3].Content = []byte(`{"outbounds":[{"protocol":"invalid-demo-protocol"}]}`)
	if err := runner.Run(t.Context(), "one.example", candidate); !errors.Is(err, preparation.ErrRejected) {
		t.Fatalf("negative catalog = %v", err)
	}
	candidate = validatorFixture(t, os.Getenv("XKEEN_TEST_CATALOG"))
	candidate.Files[4].Content = []byte(`{"routing":{"rules":[{"type":"field","ip":["geoip:fixture-away"],"outboundTag":"direct"}]}}`)
	if err := runner.Run(t.Context(), "one.example", candidate); !errors.Is(err, preparation.ErrUnavailable) {
		t.Fatalf("missing trusted local asset = %v", err)
	}
}

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == WorkerFlag {
		os.Exit(Worker(os.Args[2:]))
	}
	os.Exit(m.Run())
}
