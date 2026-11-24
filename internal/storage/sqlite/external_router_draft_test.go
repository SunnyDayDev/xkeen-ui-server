package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
)

func TestExternalRouterDraftRechecksPermitAndVersionsInWriteTransaction(t *testing.T) {
	ctx := t.Context()
	db, _, owner, issued := fixtureWebTemplateWriter(t)
	plan := storeCurrentPublication(t, db)
	id := plan.Settings.ID
	if _, err := db.ExecContext(ctx, `INSERT INTO agent_contacts (router_id,contact_ns,report,applied,uncertain)
		VALUES (?, ?,NULL,0,0)`, id, issued.UnixNano()); err != nil {
		t.Fatal(err)
	}
	store := NewExternalAccess(db, fixedWebTime(issued.Add(time.Minute)))
	policy, _, err := store.Owner(ctx, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	policy, _, err = store.Owner(ctx, owner, &externalaccess.Change{Version: policy.Version, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	permit, err := store.Admit(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	input, err := store.ReadExternalRouterDraft(ctx, permit, id)
	if err != nil {
		t.Fatal(err)
	}
	policy, _, err = store.Owner(ctx, owner, &externalaccess.Change{Version: policy.Version, Enabled: true, Protected: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadExternalRouterDraft(ctx, permit, id); !errors.Is(err, externalaccess.ErrUnauthorized) {
		t.Fatalf("read accepted a revoked permit: %v", err)
	}
	if _, err := store.WriteExternalRouterDraft(ctx, permit, input.Basis, input.Editable); !errors.Is(err, externalaccess.ErrUnauthorized) {
		t.Fatalf("writer accepted a revoked permit: %v", err)
	}
	rules, err := routerdraft.PrepareRules(input)
	if err != nil {
		t.Fatal(err)
	}
	withRule, orderBasis, err := rules.Insert("550e8400-e29b-41d4-a716-446655440010", "", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteExternalRouterOrder(ctx, permit, input.Basis, withRule, orderBasis); !errors.Is(err, externalaccess.ErrUnauthorized) {
		t.Fatalf("order writer accepted a revoked permit: %v", err)
	}
	_, secret, err := store.Owner(ctx, owner, &externalaccess.Change{Version: policy.Version, Rotate: true})
	if err != nil {
		t.Fatal(err)
	}
	permit, err = store.Admit(ctx, []string{"Bearer " + secret})
	if err != nil {
		t.Fatal(err)
	}
	if current, err := store.ReadExternalRouterDraft(ctx, permit, id); err != nil || current.Basis != input.Basis {
		t.Fatalf("revoked operation changed the router draft: %+v / %v", current.Basis, err)
	}

	results := make(chan error, 2)
	for _, name := range []string{"first candidate", "second candidate"} {
		go func() {
			draft := input.Editable
			draft.Name = name
			_, err := store.WriteExternalRouterDraft(ctx, permit, input.Basis, draft)
			results <- err
		}()
	}
	a, b := <-results, <-results
	if !(a == nil && errors.Is(b, routerdraft.ErrConflict) || b == nil && errors.Is(a, routerdraft.ErrConflict)) {
		t.Fatalf("same-generation writes must have one winner: %v / %v", a, b)
	}
	current, err := store.ReadExternalRouterDraft(ctx, permit, id)
	if err != nil || current.Basis.DraftGeneration != 1 {
		t.Fatalf("unexpected generation: %+v / %v", current.Basis, err)
	}
	current.Accepted.Version++
	storeRawAccepted(t, db, current.Accepted)
	if _, err := store.WriteExternalRouterDraft(ctx, permit, current.Basis, current.Editable); !errors.Is(err, routerdraft.ErrConflict) {
		t.Fatalf("accepted version change did not invalidate write: %v", err)
	}
	now, err := store.ReadExternalRouterDraft(ctx, permit, id)
	if err != nil || now.Basis.DraftGeneration != 1 {
		t.Fatalf("stale write changed the draft: %+v / %v", now.Basis, err)
	}
	if err := NewRouterVisibility(db).Hide(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadExternalRouterDraft(ctx, permit, id); !errors.Is(err, routerdraft.ErrMissing) {
		t.Fatalf("hidden router read: %v", err)
	}
	if _, err := store.WriteExternalRouterDraft(ctx, permit, now.Basis, now.Editable); !errors.Is(err, routerdraft.ErrMissing) {
		t.Fatalf("hidden router mutation: %v", err)
	}
}
