package sqlite

import (
	"testing"
)

func TestWatchPublicationSignalFollowsCommit(t *testing.T) {
	db, _ := openPublicationDB(t)
	defer db.Close()
	plan := storeCurrentPublication(t, db)
	var seen []string
	store := NewPublications(db, func(id string) {
		revision, err := NewPublications(db).ReadDesired(t.Context(), id)
		if err != nil || revision.Number != 2 {
			t.Fatal("notification preceded commit")
		}
		seen = append(seen, id)
	})
	if _, err := store.Commit(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(t.Context(), plan); err == nil {
		t.Fatal("stale commit succeeded")
	}
	plan.Expected.DesiredNumber, plan.Expected.ActiveGeneration = 2, 2
	plan.NewRevision = nil
	if _, err := store.Commit(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != plan.Expected.RouterID {
		t.Fatal(seen)
	}
}

func TestWatchGroupSignalsOnlyCommittedChanges(t *testing.T) {
	db, _ := openPublicationDB(t)
	defer db.Close()
	storeGroupPublication(t, db)
	plan := groupPlanFromRead(t, db, "A")
	plan.Members[0].NewRevision = groupFiles("{ }")
	var seen []string
	store := NewPublications(db, func(id string) {
		revision, err := NewPublications(db).ReadDesired(t.Context(), id)
		if err != nil || revision == nil || revision.Number != 2 {
			t.Fatal("group notification preceded commit")
		}
		seen = append(seen, id)
	})
	execSQL(t, db, `CREATE TRIGGER reject_watch_commit BEFORE INSERT ON desired_router_revisions
 BEGIN SELECT RAISE(ABORT, 'fixture'); END`)
	if _, err := store.CommitTemplateGroup(t.Context(), plan); err == nil || len(seen) != 0 {
		t.Fatal("failed commit sent a signal")
	}
	execSQL(t, db, "DROP TRIGGER reject_watch_commit")
	if _, err := store.CommitTemplateGroup(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != plan.Members[0].Expected.RouterID {
		t.Fatal(seen)
	}
}
