package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

func TestContactPersistsAndDoesNotLowerConfirmation(t *testing.T) {
	ctx := t.Context()
	db, path := openPublicationDB(t)
	plan := storeCurrentPublication(t, db)
	if _, err := NewPublications(db).Commit(ctx, plan); err != nil {
		t.Fatal(err)
	}
	keys := agentcredentials.NewService(NewAgentCredentials(db), time.Now)
	invite, err := keys.Issue(ctx, "router", agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	id, err := keys.Enroll(ctx, invite, agentcredential.Generate(agentcredential.Working).Encode())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	svc := NewContacts(db, func() time.Time { return now })
	if _, err := svc.Cursor(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.Status(ctx, id.RouterID); err != nil || got.Presence != reporting.Unobserved {
		t.Fatalf("before: %#v %v", got, err)
	}
	if err := svc.Accept(ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	report := reporting.Report{Sequence: 1, Revision: 2, State: "applied"}
	if err := svc.Accept(ctx, id, &report); err != nil {
		t.Fatal(err)
	}
	if err := svc.Accept(ctx, id, &report); err != nil {
		t.Fatal(err)
	}
	wrong := report
	// Another authenticated router has a different revision history.
	if _, err := NewPublications(db).Commit(ctx, publication.CommitPlan{
		FirstConfirmation: true,
		Expected:          publication.Expectations{RouterID: "other-router", TemplateID: plan.Expected.TemplateID, AcceptedVersion: 1},
		Settings:          routersettings.Draft{ID: "other-router", TemplateID: plan.Expected.TemplateID},
		NewRevision:       plan.NewRevision,
	}); err != nil {
		t.Fatal(err)
	}
	otherInvite, err := keys.Issue(ctx, "other-router", agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := keys.Enroll(ctx, otherInvite, agentcredential.Generate(agentcredential.Working).Encode())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Accept(ctx, otherID, &report); !errors.Is(err, reporting.ErrUnknownRevision) {
		t.Fatalf("cross-scope revision: %v", err)
	}
	wrong.Revision = 1
	if err := svc.Accept(ctx, id, &wrong); !errors.Is(err, reporting.ErrConflict) {
		t.Fatalf("conflict: %v", err)
	}
	wrong.Sequence = 2
	wrong.Revision = 100
	if err := svc.Accept(ctx, id, &wrong); !errors.Is(err, reporting.ErrUnknownRevision) {
		t.Fatalf("unknown revision: %v", err)
	}
	wrong.Revision = 1
	if err := svc.Accept(ctx, id, &wrong); err != nil {
		t.Fatal(err)
	}
	rollback := reporting.Report{Sequence: 3, Revision: 1, State: "failed", Stage: "rollback", Code: "rollback_failed"}
	if err := svc.Accept(ctx, id, &rollback); err != nil {
		t.Fatal(err)
	}
	wrong.Sequence = 4
	if err := svc.Accept(ctx, id, &wrong); err != nil {
		t.Fatal(err)
	}
	if err := svc.Accept(ctx, id, &report); err != nil {
		t.Fatal(err)
	}
	other, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	now = now.Add(179 * time.Second)
	svc = NewContacts(other, func() time.Time { return now })
	got, err := svc.Status(ctx, id.RouterID)
	if err != nil || got.Presence != reporting.Online || got.Desired != 2 || got.Applied != 2 || got.Runtime != "unknown" || got.Last.Sequence != 4 {
		t.Fatalf("179 seconds: %#v %v", got, err)
	}
	now = now.Add(time.Second)
	got, err = svc.Status(ctx, id.RouterID)
	if err != nil || got.Presence != reporting.Offline {
		t.Fatalf("180 seconds: %#v %v", got, err)
	}
	if err := keys.Revoke(ctx, id.RouterID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Accept(ctx, id, nil); !errors.Is(err, agentcredentials.ErrUnauthorized) {
		t.Fatalf("revoked write: %v", err)
	}
	if _, err := svc.Cursor(ctx, id); !errors.Is(err, agentcredentials.ErrUnauthorized) {
		t.Fatalf("revoked cursor: %v", err)
	}
}
