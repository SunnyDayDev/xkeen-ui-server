package statefile_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
)

func TestStateRoundTripAndExclusiveOwnership(t *testing.T) {
	dir := privateDirectory(t)
	store, err := statefile.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if locked, err := statefile.Open(dir); !errors.Is(err, enrollment.ErrState) {
		if locked != nil {
			locked.Close()
		}
		t.Fatalf("concurrent state open: %v", err)
	}
	if data, err := store.Load(); err != nil || data != nil {
		t.Fatalf("empty state: %v", err)
	}
	invitation := agentcredential.Generate(agentcredential.Invitation).Encode()
	if err := os.WriteFile(filepath.Join(dir, statefile.InvitationName), []byte(invitation+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Invitation(); err != nil || got != invitation {
		t.Fatalf("invitation read: %v", err)
	}
	want := enrollment.State{
		Origin: "https://server.example", Key: agentcredential.Generate(agentcredential.Working).Encode(),
		RouterID: "router-example",
	}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, statefile.StateName))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("stored credential permissions: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := statefile.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, err := reopened.Load(); err != nil || got == nil || *got != want {
		t.Fatalf("reopened credential state: %v", err)
	}
}

func TestStateRejectsUnsafeFilesystemEntries(t *testing.T) {
	t.Run("public directory", func(t *testing.T) {
		dir := privateDirectory(t)
		if err := os.Chmod(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := statefile.Open(dir); !errors.Is(err, enrollment.ErrState) {
			t.Fatalf("public state directory: %v", err)
		}
	})
	t.Run("directory symlink", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(t.TempDir(), link); err != nil {
			t.Fatal(err)
		}
		if _, err := statefile.Open(link); !errors.Is(err, enrollment.ErrState) {
			t.Fatalf("linked state directory: %v", err)
		}
	})
	t.Run("lock symlink", func(t *testing.T) {
		dir := privateDirectory(t)
		target := filepath.Join(t.TempDir(), "lock-target")
		if err := os.WriteFile(target, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, ".lock")); err != nil {
			t.Fatal(err)
		}
		if _, err := statefile.Open(dir); !errors.Is(err, enrollment.ErrState) {
			t.Fatalf("linked state lock: %v", err)
		}
	})
	for _, name := range []string{statefile.StateName, statefile.InvitationName} {
		for _, kind := range []string{"symlink", "fifo", "public", "directory"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				dir := privateDirectory(t)
				path := filepath.Join(dir, name)
				var err error
				switch kind {
				case "symlink":
					err = os.Symlink(filepath.Join(t.TempDir(), "missing"), path)
				case "fifo":
					err = unix.Mkfifo(path, 0600)
				case "public":
					err = os.WriteFile(path, []byte("public fixture"), 0644)
				case "directory":
					err = os.Mkdir(path, 0700)
				}
				if err != nil {
					t.Fatal(err)
				}
				store, err := statefile.Open(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				if name == statefile.StateName {
					if got, err := store.Load(); got != nil || !errors.Is(err, enrollment.ErrState) {
						t.Fatalf("unsafe state: %v", err)
					}
				} else if token, err := store.Invitation(); token != "" || !errors.Is(err, enrollment.ErrInvitation) {
					t.Fatalf("unsafe invitation: %v", err)
				}
			})
		}
	}
}

func privateDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
