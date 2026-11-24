//go:build linux

package localvalidation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileRejectsOverlongDigest(t *testing.T) {
	if _, ok := decodeDigest(strings.Repeat("12", 64)); ok {
		t.Fatal("overlong trust digest was accepted")
	}
}

func TestRecoveryRefusesUnownedStageWithoutRemovingData(t *testing.T) {
	group := os.Getenv("XKEEN_TEST_CGROUP")
	if group == "" || os.Geteuid() != 0 {
		t.Skip("requires disposable delegated cgroup")
	}
	stage := t.TempDir()
	foreign := filepath.Join(stage, "job-123")
	if err := os.Mkdir(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(foreign, "unowned")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := Runner{StageDir: stage, CgroupDir: group}
	lease, err := runner.Acquire(t.Context())
	if lease != nil {
		lease.Close()
	}
	if got, readErr := os.ReadFile(sentinel); err == nil || readErr != nil || string(got) != "preserve" {
		t.Fatalf("recovery of unowned stage error=%v, read error=%v", err, readErr)
	}
}
