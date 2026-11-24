package sqlite

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

func TestRouterVisibilityPreservesIdentityAndReturnsOnAcceptedContact(t *testing.T) {
	ctx := t.Context()
	db, path := openPublicationDB(t)
	plan := storeCurrentPublication(t, db)
	if _, err := NewPublications(db).Commit(ctx, plan); err != nil {
		t.Fatal(err)
	}
	before, err := NewPublications(db).Read(ctx, publication.ReadRequest{RouterID: "router"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000000000, 0)
	clock := func() time.Time { return now }
	keys := agentcredentials.NewService(NewAgentCredentials(db), clock)
	invite, err := keys.Issue(ctx, "router", agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	working := agentcredential.Generate(agentcredential.Working).Encode()
	id, err := keys.Enroll(ctx, invite, working)
	if err != nil {
		t.Fatal(err)
	}
	web := NewWebAdmin(db)
	if err := web.SaveVerifier(ctx, webadmin.Verifier{Version: 1}, false); err != nil {
		t.Fatal(err)
	}
	digest := [32]byte{1}
	if err := web.Issue(ctx, 1, digest, [32]byte{}, now); err != nil {
		t.Fatal(err)
	}

	view := routersettings.NewVisibility(NewRouterVisibility(db))
	check := func(want ...string) {
		t.Helper()
		ids, err := view.VisibleIDs(ctx)
		if err != nil || ids == nil || len(ids) != len(want) || (len(ids) > 0 && !reflect.DeepEqual(ids, want)) {
			t.Fatalf("visible %q, want %q (%v)", ids, want, err)
		}
	}
	check()
	if err := view.Hide(ctx, "Router"); !errors.Is(err, routersettings.ErrVisibilityNotFound) {
		t.Fatalf("unexpected case alias: %v", err)
	}
	if err := view.Hide(ctx, ""); !errors.Is(err, routersettings.ErrVisibilityInvalid) {
		t.Fatalf("empty ID: %v", err)
	}
	contacts := NewContacts(db, clock)
	latest := reporting.Report{Sequence: 5, Revision: 2, State: "applied"}
	if err := contacts.Accept(ctx, id, &latest); err != nil {
		t.Fatal(err)
	}
	check("router")
	hide := func() {
		t.Helper()
		if err := view.Hide(ctx, id.RouterID); err != nil {
			t.Fatal(err)
		}
		check()
	}
	hide()
	hide()
	if identity, err := keys.Identity(ctx, working); err != nil || identity != id {
		t.Fatal("hide changed working identity")
	}
	if cursor, err := contacts.Cursor(ctx, id); err != nil || cursor != 5 {
		t.Fatal("hide changed report cursor")
	}
	if err := web.Session(ctx, digest, clock, webadmin.SessionRead); err != nil {
		t.Fatal("hide changed active browser session")
	}
	got, err := NewPublications(db).Read(ctx, publication.ReadRequest{RouterID: id.RouterID})
	if err != nil || !reflect.DeepEqual(got, before) {
		t.Fatal("hide changed settings or publication history")
	}
	wrong := latest
	wrong.Revision = 1
	if err := contacts.Accept(ctx, id, &wrong); !errors.Is(err, reporting.ErrConflict) {
		t.Fatalf("conflicting replay: %v", err)
	}
	check()
	if err := contacts.Accept(ctx, id, &latest); err != nil {
		t.Fatal(err)
	}
	check("router")
	hide()
	stale := wrong
	stale.Sequence--
	if err := contacts.Accept(ctx, id, &stale); err != nil {
		t.Fatal(err)
	}
	check("router")
	status, err := contacts.Status(ctx, id.RouterID)
	if err != nil || status.Applied != 2 || status.Last == nil || *status.Last != latest {
		t.Fatal("stale contact changed the report")
	}
	hide()
	other, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	view = routersettings.NewVisibility(NewRouterVisibility(other))
	check()
	contacts = NewContacts(other, clock)
	if err := contacts.Accept(ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	check("router")
	hide()
	if err := keys.Revoke(ctx, id.RouterID); err != nil {
		t.Fatal(err)
	}
	if err := contacts.Accept(ctx, id, nil); !errors.Is(err, agentcredentials.ErrUnauthorized) {
		t.Fatalf("revoked contact: %v", err)
	}
	check()
}

func TestRouterVisibilityRollsBackWhenRemovingHiddenMarkerFails(t *testing.T) {
	ctx := t.Context()
	db, _ := openPublicationDB(t)
	plan := storeCurrentPublication(t, db)
	if _, err := NewPublications(db).Commit(ctx, plan); err != nil {
		t.Fatal(err)
	}
	keys := agentcredentials.NewService(NewAgentCredentials(db), time.Now)
	token, err := keys.Issue(ctx, "router", agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	id, err := keys.Enroll(ctx, token, agentcredential.Generate(agentcredential.Working).Encode())
	if err != nil {
		t.Fatal(err)
	}
	view := routersettings.NewVisibility(NewRouterVisibility(db))
	if err := view.Hide(ctx, id.RouterID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fixture_fail_unhide BEFORE DELETE ON hidden_router_visibility
		BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	contacts := NewContacts(db, time.Now)
	if err := contacts.Accept(ctx, id, &reporting.Report{Sequence: 1, Revision: 2, State: "applied"}); !errors.Is(err, reporting.ErrUnavailable) {
		t.Fatalf("failed transaction: %v", err)
	}
	if cursor, err := contacts.Cursor(ctx, id); err != nil || cursor != 0 {
		t.Fatal("contact report survived rollback")
	}
	status, err := contacts.Status(ctx, id.RouterID)
	if err != nil || status.Presence != reporting.Unobserved || status.Applied != 0 {
		t.Fatal("contact state survived rollback")
	}
	if ids, err := view.VisibleIDs(ctx); err != nil || len(ids) != 0 {
		t.Fatal("router returned after failed transaction")
	}
	var hidden bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hidden_router_visibility WHERE router_id='router')`).Scan(&hidden); err != nil || !hidden {
		t.Fatal("failed contact removed the hidden marker")
	}
}

func TestRouterVisibilityMigrationKeepsExistingContact(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "old.sqlite")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	// Construct the exact previously supported schema without the new table.
	if err := migrate(ctx, old, migrations[:len(migrations)-1]); err != nil {
		t.Fatal(err)
	}
	plan := storeCurrentPublication(t, old)
	if _, err := NewPublications(old).Commit(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := old.ExecContext(ctx, `INSERT INTO agent_contacts(router_id,contact_ns,report,applied,uncertain)
		VALUES('router',?,NULL,0,0)`, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if ids, err := NewRouterVisibility(db).VisibleIDs(ctx); err != nil || !reflect.DeepEqual(ids, []string{"router"}) {
		t.Fatalf("migration changed existing projection: %q, %v", ids, err)
	}
	if status, err := NewContacts(db, time.Now).Status(ctx, "router"); err != nil || status.Presence != reporting.Online || status.Desired != 2 {
		t.Fatal("migration changed existing contact")
	}
}
