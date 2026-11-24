//go:build linux

package localvalidation

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkingManifestRejectsUnexpectedLoadedFilesAndPinsBytes(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("working directory is root-administered")
	}
	root := t.TempDir()
	known := filepath.Join(root, "01_log.json")
	if err := os.WriteFile(known, []byte(" {}\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("preserved"), 0640); err != nil {
		t.Fatal(err)
	}
	one, err := WorkingManifest(root)
	if err != nil || one == "" {
		t.Fatalf("manifest=%q, %v", one, err)
	}
	if err := os.Chmod(known, 0600); err != nil {
		t.Fatal(err)
	}
	two, err := WorkingManifest(root)
	if err != nil || one == two {
		t.Fatalf("permissions change not pinned: %v", err)
	}
	bad := filepath.Join(root, "unknown.JSONC")
	if err := os.WriteFile(bad, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := WorkingManifest(root); err == nil {
		t.Fatal("unexpected loaded file was accepted")
	}
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(known); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "notes.txt"), known); err != nil {
		t.Fatal(err)
	}
	if _, err := WorkingManifest(root); err == nil {
		t.Fatal("managed symlink was accepted")
	}
}
