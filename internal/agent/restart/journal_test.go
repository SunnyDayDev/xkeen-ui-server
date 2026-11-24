package restart

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

func TestJournalRecoversPendingWithoutOriginalProcess(t *testing.T) {
	f := newApplyFixture(t)

	dir := privateTestDirectory(t)
	f.old.Files[0].Content = []byte(" { \"log\": \"<x>\" }\n")
	f.first.Snapshot.Files[0].Content = f.old.Files[0].Content
	tag, err := agentrevision.Tag(f.scope.RouterID, f.first.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	f.first.Tag = tag
	f.record.Pending = &Intent{AttemptID: "01010101010101010101010101010101", Previous: f.old, Target: f.first, TargetFiles: f.old.Files, Phase: Planned}
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

func TestJournalResyncsVisibleUnconfirmedIntent(t *testing.T) {
	f := newApplyFixture(t)
	f.record.Pending = &Intent{AttemptID: "01010101010101010101010101010101", Previous: f.old,
		Target: f.first, TargetFiles: f.old.Files, Phase: Planned}
	journal, err := OpenJournal(privateTestDirectory(t), f.scope)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	sync := journal.syncDir
	journal.syncDir = func() error { return ErrJournal }
	if err := journal.Save(f.record); err != ErrJournal {
		t.Fatal("late fsync failure was mistaken for confirmed intent", err)
	}
	if _, err := journal.Load(); err != ErrJournal {
		t.Fatal("visible intent bypassed the durability barrier", err)
	}
	journal.syncDir = sync
	got, err := journal.Load()
	if err != nil || !reflect.DeepEqual(got, f.record) {
		t.Fatal("successful barrier did not restore intact intent", err)
	}
}

func TestJournalPreRenameSyncFailureKeepsPreviousRecord(t *testing.T) {
	f := newApplyFixture(t)
	journal, err := OpenJournal(privateTestDirectory(t), f.scope)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if err := journal.Save(f.record); err != nil {
		t.Fatal(err)
	}
	next := f.record
	next.Pending = &Intent{AttemptID: "01010101010101010101010101010101", Previous: f.old,
		Target: f.first, TargetFiles: f.old.Files, Phase: Planned}
	journal.syncFile = func(*os.File) error { return ErrJournal }
	if err := journal.Save(next); err != ErrJournal {
		t.Fatal("unsynced temporary file completed intent", err)
	}
	got, err := journal.Load()
	if err != nil || !reflect.DeepEqual(got, f.record) {
		t.Fatal("unconfirmed temporary write changed the previous journal", err)
	}
}
