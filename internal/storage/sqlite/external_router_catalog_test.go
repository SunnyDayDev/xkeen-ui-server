package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
)

func TestExternalRouterCatalogRechecksPermitAfterReading(t *testing.T) {
	ctx := t.Context()
	db, _, owner, issued := fixtureWebTemplateWriter(t)
	published := storeCurrentPublication(t, db)
	id := published.Settings.ID
	if _, err := db.ExecContext(ctx, `INSERT INTO agent_contacts(router_id,contact_ns,report,applied,uncertain)
		VALUES (?, ?,NULL,0,0)`, id, issued.UnixNano()); err != nil {
		t.Fatal(err)
	}
	access := NewExternalAccess(db, fixedWebTime(issued.Add(time.Minute)))
	policy, _, err := access.Owner(ctx, owner, &externalaccess.Change{Version: 1, Enabled: true, Protected: false})
	if err != nil {
		t.Fatal(err)
	}
	permit, err := access.Admit(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := access.ReadExternalRouterCatalog(ctx, permit, id, publication.PreviewDesired)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := publication.RouterPreviewExpectations(inputs, publication.PreviewDesired)
	if err != nil {
		t.Fatal(err)
	}
	if err := access.CheckExternalRouterCatalog(ctx, permit, publication.PreviewDesired, expected); err != nil {
		t.Fatal(err)
	}
	if _, _, err := access.Owner(ctx, owner, &externalaccess.Change{Version: policy.Version, Rotate: true}); err != nil {
		t.Fatal(err)
	}
	if err := access.CheckExternalRouterCatalog(ctx, permit, publication.PreviewDesired, expected); !errors.Is(err, externalaccess.ErrUnauthorized) {
		t.Fatalf("late revoked permit returned %v", err)
	}
	if out, err := access.ReadExternalRouterCatalog(ctx, permit, id, publication.PreviewDesired); out != nil || !errors.Is(err, externalaccess.ErrUnauthorized) {
		t.Fatalf("stale admission reached protected read: %v %v", out, err)
	}
}
