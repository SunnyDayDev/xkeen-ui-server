package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestExternalRouterPreparationRechecksRevokedTokenAfterValidation(t *testing.T) {
	ctx := t.Context()
	db, _, owner, issued := fixtureWebTemplateWriter(t)
	pubs := NewPublications(db)
	access := NewExternalAccess(db, fixedWebTime(issued.Add(time.Minute)))
	enabled, _, err := access.Owner(ctx, owner, &externalaccess.Change{Version: 1, Enabled: true, Protected: true})
	if err != nil {
		t.Fatal(err)
	}
	policy, token, err := access.Owner(ctx, owner, &externalaccess.Change{Version: enabled.Version, Rotate: true})
	if err != nil {
		t.Fatal(err)
	}
	permit, err := access.Admit(ctx, []string{"Bearer " + token})
	if err != nil {
		t.Fatal(err)
	}
	const router = "550e8400-e29b-41d4-a716-446655440001"
	const template = "synthetic-pending-router"
	_, err = templates.New(NewTemplateRepository(db)).Create(ctx, configuration.TemplateDraft{ID: template, Sections: []configuration.TemplateSection{{
		Name: "outbounds", Elements: []configuration.TemplateElement{{Element: configuration.Element{
			ID: "550e8400-e29b-41d4-a716-446655440000", Content: `{"protocol":"freedom","tag":"direct"}`,
		}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	validation := testPublicationValidator(func(ctx context.Context, _ string, _ []configuration.CatalogFile) error {
		called++
		_, _, err := access.Owner(ctx, owner, &externalaccess.Change{Version: policy.Version, Rotate: true})
		return err
	})
	service := externalaccess.NewRouterPreparation(NewExternalRouterPreparation(pubs), validation)
	before, err := service.Inspect(ctx, permit, router, template)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := service.Prepare(ctx, permit, before, "Example", nil)
	if err == nil || !errors.Is(err, externalaccess.ErrUnauthorized) || receipt != (externalaccess.PreparationReceipt{}) || called != 1 {
		t.Fatalf("revoked preparation returned %+v, %v, validator calls=%d", receipt, err, called)
	}
	desired, err := pubs.ReadDesired(ctx, router)
	if err != nil || desired != nil {
		t.Fatalf("stale authorization persisted a desired revision: %v", err)
	}
	var accepted int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM accepted_templates WHERE template_id=?", template).Scan(&accepted); err != nil || accepted != 0 {
		t.Fatalf("stale authorization accepted a template: %v", err)
	}
}
