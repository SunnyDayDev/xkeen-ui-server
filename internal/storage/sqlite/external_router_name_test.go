package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
)

func TestExternalRouterNameUpdatesOwnerProjectionOnlyWhileAdmitted(t *testing.T) {
	ctx := t.Context()
	db, _, session, now := fixtureWebTemplateWriter(t)
	clock := func() time.Time { return now }
	plan := storeCurrentPublication(t, db)
	if _, err := NewPublications(db).Commit(ctx, plan); err != nil {
		t.Fatal(err)
	}
	keys := agentcredentials.NewService(NewAgentCredentials(db), clock)
	invite, err := keys.Issue(ctx, "router", agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	id, err := keys.Enroll(ctx, invite, agentcredential.Generate(agentcredential.Working).Encode())
	if err != nil {
		t.Fatal(err)
	}
	if err := NewContacts(db, clock).Accept(ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	ext := NewExternalAccess(db, clock)
	if _, err := db.ExecContext(ctx, "UPDATE external_access SET enabled=1,protected=0,version=2"); err != nil {
		t.Fatal(err)
	}
	permit := externalaccess.Permit{Version: 2}
	legacy, err := ext.ReadExternalRouterName(ctx, permit, id.RouterID)
	if err != nil || legacy.Version != 0 || legacy.Name != "new" {
		t.Fatalf("old display name unavailable: %+v, %v", legacy, err)
	}
	const renamed = "Обновлённое имя"
	next, err := ext.WriteExternalRouterName(ctx, permit, legacy, renamed)
	if err != nil || next.Version != 1 || next.ActiveGeneration != legacy.ActiveGeneration {
		t.Fatalf("invalid display metadata commit: %+v, %v", next, err)
	}
	rows, err := NewWebRouterStatus(db).List(ctx, session, clock)
	if err != nil || len(rows) != 1 || rows[0].Name != renamed {
		t.Fatalf("F11 did not use independent display metadata: %+v, %v", rows, err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE external_access SET protected=1,version=3"); err != nil {
		t.Fatal(err)
	}
	if _, err := ext.WriteExternalRouterName(ctx, permit, next, "other name"); !errors.Is(err, externalaccess.ErrUnauthorized) {
		t.Fatal("write accepted a stale permit")
	}
	rows, err = NewWebRouterStatus(db).List(ctx, session, clock)
	if err != nil || len(rows) != 1 || rows[0].Name != renamed {
		t.Fatal("failed write changed the display name")
	}
}
