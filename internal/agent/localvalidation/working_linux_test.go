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
	toml := filepath.Join(root, "extra.toml")
	if err := os.WriteFile(toml, []byte("[routing]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := WorkingManifest(root); err == nil {
		t.Fatal("additional TOML loaded by pinned Xray was accepted")
	}
	if err := os.Remove(toml); err != nil {
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

func TestWorkingArgumentsUseOneExactConfdir(t *testing.T) {
	dir := "/opt/etc/xray/configs"
	for _, tc := range []struct {
		args []string
		env  string
		want bool
	}{
		{[]string{"run"}, "XRAY_LOCATION_CONFDIR=" + dir + "\x00", true},
		{[]string{"run", "-confdir", dir}, "", true},
		{[]string{"run", "-confdir", dir, "-c", "another.json"}, "", false},
		{[]string{"run"}, "XRAY_LOCATION_CONFDIR=" + dir + "\x00xray.location.confdir=/other\x00", false},
		{[]string{"run"}, "XRAY_LOCATION_CONFDIR=/other\x00", false},
	} {
		if got := workingArguments(tc.args, []byte(tc.env), dir); got != tc.want {
			t.Errorf("working launch verification=%t, want %t", got, tc.want)
		}
	}
}
