package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEntwareUpdateRecoveryRequiresDurableCurrentAndCompleteIntent(t *testing.T) {
	if os.Getenv("XKEEN_TEST_ENTWARE_UPDATE_RECOVERY") != "1" {
		t.Skip("run lab/check-entware-install.sh in isolated Entware")
	}
	if err := rootDirs(true, control, executableBase, executableBase+"/generations"); err != nil {
		t.Fatal(err)
	}
	base := profile{Version: "v0.0.13", Manifest: strings.Repeat("a", 64), Origin: "https://controller.example", UID: 65532, GID: 65532}
	next := base
	next.Version, next.Manifest = "v0.0.14", strings.Repeat("b", 64)
	beforeDir := filepath.Join(executableBase, "generations", base.Version)
	nextDir := filepath.Join(executableBase, "generations", next.Version)
	if err := rootDirs(true, beforeDir, nextDir); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{beforeDir, nextDir} {
		if err := writeFile(filepath.Join(dir, agentAsset), []byte("stopped fixture"), 0755, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(release{next.Version, next.Manifest})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(nextDir, "release.json"), raw, 0600, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := pointTo(next); err != nil {
		t.Fatal(err)
	}
	if err := saveUpdate(updateIntent{Before: base, Next: next}); err != nil {
		t.Fatal(err)
	}

	t.Run("retry after ambiguous sync", func(t *testing.T) {
		realSync := syncCurrent
		defer func() { syncCurrent = realSync }()
		syncCurrent = func() error {
			if got, _ := os.Readlink(current); got == "generations/"+base.Version {
				return errors.New("injected post-rename sync failure")
			}
			return realSync()
		}
		if !errors.Is(recoverUpdate(base), errRecovery) {
			t.Fatal("failed rollback sync was not reported")
		}
		if got, err := activeProfile(base); err != nil || got != base {
			t.Fatal("fixture did not expose old pointer after failed barrier")
		}
		if !errors.Is(recoverUpdate(base), errRecovery) {
			t.Fatal("retry accepted a pointer that had not been synced")
		}
		if _, err := os.Stat(updatePath); err != nil {
			t.Fatal("retry removed its only recovery intent")
		}
		syncCurrent = realSync
		if err := recoverUpdate(base); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(updatePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("durable recovery did not clear the intent")
		}

	})

	for label, raw := range map[string]map[string]any{
		"missing running":   {"Before": base, "Next": next, "Committed": false},
		"missing committed": {"Before": base, "Next": next, "WasRunning": false},
	} {
		t.Run(label, func(t *testing.T) {
			body, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeFile(updatePath, body, 0600, 0, 0); err != nil {
				t.Fatal(err)
			}
			if _, _, err := loadUpdate(base); !errors.Is(err, errRecovery) {
				t.Fatal("incomplete intent was accepted")
			}
		})
	}
}
