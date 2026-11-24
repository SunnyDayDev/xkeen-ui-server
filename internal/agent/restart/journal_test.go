package restart

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestJournalRecoversPendingWithoutOriginalProcess(t *testing.T) {
	f := newApplyFixture(t)
	dir := privateTestDirectory(t)
	f.record.Pending = &Intent{AttemptID: "01010101010101010101010101010101", Previous: f.old, Target: f.first}
	journal, err := OpenJournal(dir, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Save(f.record); err != nil {
		t.Fatal(err)
	}
	if concurrent, err := OpenJournal(dir, f.scope); err == nil {
		concurrent.Close()
		t.Fatal("parallel journal owner")
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := OpenJournal(dir, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	record, err := recovered.Load()
	if err != nil || !reflect.DeepEqual(record, f.record) {
		t.Fatal("complete prior and target snapshots are not independently recoverable", err)
	}
}

func TestJournalRejectsConflictingEntry(t *testing.T) {
	f := newApplyFixture(t)
	for _, kind := range []string{"symlink", "corrupt", "weak permissions"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateTestDirectory(t)
			path := filepath.Join(dir, journalName)
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(filepath.Join(dir, "unknown"), path)
			case "corrupt":
				err = os.WriteFile(path, []byte(`{"Version":1,"version":1}`), 0600)
			case "weak permissions":
				err = os.WriteFile(path, []byte("{}"), 0644)
			}
			if err != nil {
				t.Fatal(err)
			}
			journal, err := OpenJournal(dir, f.scope)
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			if record, err := journal.Load(); err == nil || record.Pending != nil {
				t.Fatal("unsafe record appeared readable")
			}
		})
	}
}

func privateTestDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
