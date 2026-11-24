package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type externalPreviewValidator func(context.Context, string, []configuration.CatalogFile) error

func (v externalPreviewValidator) Validate(ctx context.Context, profile string, files []configuration.CatalogFile) error {
	return v(ctx, profile, files)
}

func TestExternalPublicationPreviewChecksPermitAfterValidation(t *testing.T) {
	ctx := t.Context()
	db, _, owner, issued := fixtureWebTemplateWriter(t)
	published := storeCurrentPublication(t, db)
	clock := fixedWebTime(issued.Add(time.Minute))
	access := NewExternalAccess(db, clock)
	policy, _, err := access.Owner(ctx, owner, &externalaccess.Change{Version: 1, Enabled: true, Protected: false})
	if err != nil {
		t.Fatal(err)
	}
	permit, err := access.Admit(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	groups := NewExternalTemplatePublications(NewPublications(db))
	called := 0
	validation := externalPreviewValidator(func(ctx context.Context, _ string, _ []configuration.CatalogFile) error {
		called++
		_, _, err := access.Owner(ctx, owner, &externalaccess.Change{Version: policy.Version, Rotate: true})
		return err
	})
	service := externalaccess.NewTemplatePublications(groups, validation, clock)
	result, err := service.Preview(ctx, permit, published.Settings.TemplateID, 1)
	if result != nil || called != 1 || !errors.Is(err, externalaccess.ErrUnauthorized) {
		t.Fatalf("late revoked preview %v, %v, validation=%d", result, err, called)
	}
	current, err := NewPublications(db).ReadDesired(ctx, published.Settings.ID)
	if err != nil || current.Number != 1 {
		t.Fatalf("preview changed desired: %v %v", current, err)
	}
}
