package sqlite

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

func TestWebRouterStatusFollowsAcceptedContactAndVisibility(t *testing.T) {
	ctx := t.Context()
	db, _, digest, issued := fixtureWebTemplateWriter(t)
	now := issued
	clock := func() time.Time { return now }
	plan := storeCurrentPublication(t, db)
	if _, err := NewPublications(db).Commit(ctx, plan); err != nil {
		t.Fatal(err)
	}
	status := NewWebRouterStatus(db)
	read := func(want int) []webadmin.RouterStatus {
		t.Helper()
		rows, err := status.List(ctx, digest, clock)
		if err != nil || len(rows) != want || rows == nil {
			t.Fatalf("rows: %+v %v", rows, err)
		}
		return rows
	}
	read(0)
	keys := agentcredentials.NewService(NewAgentCredentials(db), clock)
	invite, err := keys.Issue(ctx, "router", agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	id, err := keys.Enroll(ctx, invite, agentcredential.Generate(agentcredential.Working).Encode())
	if err != nil {
		t.Fatal(err)
	}
	read(0)
	contacts := NewContacts(db, clock)
	if err := contacts.Accept(ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	if row := read(1)[0]; row.Contact != reporting.Online || row.Applied != 0 || row.Name != "new" || row.TemplateID != "A" || row.Desired != 2 {
		t.Fatalf("heartbeat conflated with application: %+v", row)
	}
	// The overview reads only routing metadata and does not decode payloads that
	// are irrelevant to status, even on an already observed router.
	if _, err := db.ExecContext(ctx, `UPDATE active_router_publications SET
		settings=CAST(json_set(CAST(settings AS TEXT), '$.Groups', 500) AS BLOB) WHERE router_id=?`, id.RouterID); err != nil {
		t.Fatal(err)
	}
	if row := read(1)[0]; row.Name != "new" || row.TemplateID != "A" {
		t.Fatalf("metadata lost: %+v", row)
	}
	if err := contacts.Accept(ctx, id, &reporting.Report{Sequence: 1, Revision: 1, State: "applied"}); err != nil {
		t.Fatal(err)
	}
	if err := contacts.Accept(ctx, id, &reporting.Report{Sequence: 2, Revision: 2, State: "failed", Stage: "rollback", Code: "rollback_failed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO desired_router_revisions SELECT router_id, ?, profile, files FROM desired_router_revisions WHERE router_id='router' AND revision=2", int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	now = issued.Add(179 * time.Second)
	if row := read(1)[0]; row.Contact != reporting.Online || row.Runtime != "unknown" || row.Applied != 1 || row.Desired != math.MaxInt64 {
		t.Fatalf("historical application/unknown runtime: %+v", row)
	}
	now = now.Add(time.Second)
	if row := read(1)[0]; row.Contact != reporting.Offline || row.Applied != 1 || row.Runtime != "unknown" {
		t.Fatalf("offline boundary: %+v", row)
	}
	if err := NewRouterVisibility(db).Hide(ctx, id.RouterID); err != nil {
		t.Fatal(err)
	}
	read(0)
	if err := contacts.Accept(ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	if row := read(1)[0]; row.RouterID != id.RouterID || row.Contact != reporting.Online || row.Applied != 1 {
		t.Fatalf("revived row: %+v", row)
	}
	if err := keys.Revoke(ctx, id.RouterID); err != nil {
		t.Fatal(err)
	}
	read(1)
	if _, err := db.ExecContext(ctx, `UPDATE active_router_publications SET
		settings=CAST(json_remove(CAST(settings AS TEXT), '$.Name') AS BLOB) WHERE router_id=?`, id.RouterID); err != nil {
		t.Fatal(err)
	}
	if row := read(1)[0]; row.Name != id.RouterID {
		t.Fatalf("legacy name fallback: %+v", row)
	}
	if _, err := db.ExecContext(ctx, `UPDATE active_router_publications SET
		settings=CAST(json_set(CAST(settings AS TEXT), '$.Name', 500) AS BLOB) WHERE router_id=?`, id.RouterID); err != nil {
		t.Fatal(err)
	}
	if rows, err := status.List(ctx, digest, clock); rows != nil || err == nil {
		t.Fatalf("invalid source metadata returned: %+v %v", rows, err)
	}
	external := NewExternalAccess(db, clock)
	if _, err := db.ExecContext(ctx, "UPDATE external_access SET enabled=1,protected=0,version=2"); err != nil {
		t.Fatal(err)
	}
	stale := externalaccess.Permit{Version: 1}
	if rows, err := external.ReadExternalRouterStatuses(ctx, stale); rows != nil || !errors.Is(err, externalaccess.ErrUnauthorized) {
		t.Fatal("late F20 check did not refuse the stale permit")
	}
	now = issued.Add(webadmin.IdleLifetime)
	if rows, err := status.List(ctx, digest, clock); rows != nil || !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("reads advanced session or exposed protected fields: %+v %v", rows, err)
	}
}
