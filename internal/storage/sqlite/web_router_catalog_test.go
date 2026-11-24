package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

func TestWebRouterCatalogRechecksUsedGenerationAndVisibility(t *testing.T) {
	ctx := t.Context()
	db, _, digest, issued := fixtureWebTemplateWriter(t)
	published := storeCurrentPublication(t, db)
	id := published.Settings.ID
	if _, err := db.ExecContext(ctx, `INSERT INTO agent_contacts (router_id,contact_ns,report,applied,uncertain)
		VALUES (?, ?,NULL,0,0)`, id, issued.UnixNano()); err != nil {
		t.Fatal(err)
	}
	clock := fixedWebTime(issued.Add(time.Minute))
	catalog := NewWebRouterCatalog(NewPublications(db))
	snapshot, err := catalog.ReadCatalog(ctx, digest, clock, id, publication.PreviewPersonal)
	if err != nil {
		t.Fatal(err)
	}
	personal, err := publication.RouterPreviewExpectations(snapshot, publication.PreviewPersonal)
	if err != nil {
		t.Fatal(err)
	}
	desired, err := publication.RouterPreviewExpectations(snapshot, publication.PreviewDesired)
	if err != nil {
		t.Fatal(err)
	}
	drafts := NewWebRouterDrafts(db)
	editable, err := drafts.Read(ctx, digest, clock, id)
	if err != nil {
		t.Fatal(err)
	}
	editable.Editable.Name = "changed after catalog read"
	if _, err := drafts.Write(ctx, digest, clock, editable.Basis, editable.Editable); err != nil {
		t.Fatal(err)
	}
	if err := catalog.CheckCatalog(ctx, digest, clock, publication.PreviewPersonal, personal); !errors.Is(err, publication.ErrConflict) {
		t.Fatalf("stale personal catalog returned %v", err)
	}
	if err := catalog.CheckCatalog(ctx, digest, clock, publication.PreviewDesired, desired); err != nil {
		t.Fatalf("desired wrongly depends on the unused draft: %v", err)
	}
	if err := NewRouterVisibility(db).Hide(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := catalog.CheckCatalog(ctx, digest, clock, publication.PreviewDesired, desired); !errors.Is(err, webadmin.ErrRouterDraftMissing) {
		t.Fatalf("hidden router acquired catalog: %v", err)
	}
	if err := NewWebAdmin(db).SaveVerifier(ctx, webadmin.Verifier{Version: 1}, true); err != nil {
		t.Fatal(err)
	}
	if err := catalog.CheckCatalog(ctx, digest, clock, publication.PreviewDesired, desired); !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("revoked session acquired catalog: %v", err)
	}
}
