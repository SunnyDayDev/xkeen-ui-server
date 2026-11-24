package statefile

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/desired"
)

func TestDesiredCacheIsPrivateAtomicAndReopens(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry := desired.Entry{Origin: "https://server.example", RouterID: "one.example", Tag: "tag", Body: []byte(`{"revision":"8"}`)}
	if initial, err := store.LoadDesired(); initial != nil || err != nil {
		t.Fatalf("absent cache = %v, %v", initial, err)
	}
	if err := store.SaveDesired(entry); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, DesiredName))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("cache permissions = %v, %v", info, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, DesiredName))
	if err != nil {
		t.Fatal(err)
	}
	broken := entry
	broken.Body = []byte("{")
	if err := store.SaveDesired(broken); err == nil {
		t.Fatal("invalid json encoded as a record")
	}
	if after, err := os.ReadFile(filepath.Join(dir, DesiredName)); err != nil || !bytes.Equal(raw, after) {
		t.Fatalf("failed save changed the old record: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if loaded, err := store.LoadDesired(); err != nil || !reflect.DeepEqual(loaded, &entry) {
		t.Fatalf("reopened cache = %v, %v", loaded, err)
	}
	if err := os.Chmod(filepath.Join(dir, DesiredName), 0644); err != nil {
		t.Fatal(err)
	}
	if loaded, err := store.LoadDesired(); loaded != nil || err == nil {
		t.Fatal("non-private cache was accepted")
	}
}
