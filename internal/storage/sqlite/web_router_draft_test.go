package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

func TestWebRouterDraftWriterRechecksVisibilityAndEveryGeneration(t *testing.T) {
	ctx := t.Context()
	db, _, digest, issued := fixtureWebTemplateWriter(t)
	plan := storeCurrentPublication(t, db)
	id := plan.Settings.ID
	if _, err := db.ExecContext(ctx, `INSERT INTO agent_contacts (router_id,contact_ns,report,applied,uncertain) VALUES (?, ?,NULL,0,0)`, id, issued.UnixNano()); err != nil {
		t.Fatal(err)
	}
	writer := NewWebRouterDrafts(db)
	clock := fixedWebTime(issued.Add(time.Minute))
	base, err := writer.Read(ctx, digest, clock, id)
	if err != nil {
		t.Fatal(err)
	}
	if base.Basis.RouterID != id || base.Basis.TemplateID != "A" || base.Basis.ActiveGeneration != 1 || base.Basis.AcceptedVersion != 1 || base.Basis.DraftGeneration != 0 {
		t.Fatalf("incorrect basis: %+v", base.Basis)
	}
	candidate := base.Editable
	candidate.Name = "changed draft"
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER suppress_router_draft BEFORE INSERT ON router_settings
		BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	generation, err := writer.Write(ctx, digest, clock, base.Basis, candidate)
	if err == nil || generation != 0 {
		t.Fatalf("suppressed insert reported a successful draft: generation=%d error=%v", generation, err)
	}
	if _, err := db.ExecContext(ctx, "DROP TRIGGER suppress_router_draft"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		generation int64
		err        error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			generation, err := writer.Write(ctx, digest, clock, base.Basis, candidate)
			results <- result{generation, err}
		}()
	}
	a, b := <-results, <-results
	if a.generation+b.generation != 1 ||
		!(a.err == nil && errors.Is(b.err, webadmin.ErrRouterDraftConflict) || b.err == nil && errors.Is(a.err, webadmin.ErrRouterDraftConflict)) {
		t.Fatalf("competing first writes: %v / %v", a, b)
	}
	reloaded, err := writer.Read(ctx, digest, clock, id)
	if err != nil || reloaded.Editable.Name != candidate.Name || reloaded.Basis.DraftGeneration != 1 {
		t.Fatalf("missing committed draft: %v", err)
	}
	for _, wrong := range []webadmin.RouterDraftBasis{
		{RouterID: id, TemplateID: "B", ActiveGeneration: 1, AcceptedVersion: 1, DraftGeneration: 1},
		{RouterID: id, TemplateID: "A", ActiveGeneration: 2, AcceptedVersion: 1, DraftGeneration: 1},
		{RouterID: id, TemplateID: "A", ActiveGeneration: 1, AcceptedVersion: 2, DraftGeneration: 1},
	} {
		if _, err := writer.Write(ctx, digest, clock, wrong, candidate); !errors.Is(err, webadmin.ErrRouterDraftConflict) {
			t.Fatalf("wrong scope/version accepted: %+v %v", wrong, err)
		}
	}
	if err := NewRouterVisibility(db).Hide(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(ctx, digest, clock, reloaded.Basis, candidate); !errors.Is(err, webadmin.ErrRouterDraftMissing) {
		t.Fatalf("hidden router mutation: %v", err)
	}
	if err := NewWebAdmin(db).SaveVerifier(ctx, webadmin.Verifier{Version: 1}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(ctx, digest, clock, reloaded.Basis, candidate); !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("revoked session mutation: %v", err)
	}
	saved, ok, err := NewRouterSettings(db).Get(ctx, id)
	if err != nil || !ok || saved.Name != "changed draft" {
		t.Fatalf("rejected save changed the existing draft: %v", err)
	}
}
