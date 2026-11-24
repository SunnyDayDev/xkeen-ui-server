package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
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

func TestExternalPublicationConfirmRechecksPermitAndExpiryInsideWriter(t *testing.T) {
	for _, row := range []struct {
		name string
		want error
	}{
		{"revoked", externalaccess.ErrUnauthorized},
		{"expired", publication.ErrConflict},
	} {
		t.Run(row.name, func(t *testing.T) {
			ctx := t.Context()
			db, _, owner, issued := fixtureWebTemplateWriter(t)
			published := storeCurrentPublication(t, db)
			now := issued.Add(time.Minute)
			clock := func() time.Time { return now }
			access := NewExternalAccess(db, clock)
			policy, _, err := access.Owner(ctx, owner, &externalaccess.Change{Version: 1, Enabled: true, Protected: false})
			if err != nil {
				t.Fatal(err)
			}
			permit, err := access.Admit(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			notified := 0
			groups := NewExternalTemplatePublications(NewPublications(db, func(string) { notified++ }))
			var confirming bool
			validator := externalPreviewValidator(func(ctx context.Context, _ string, _ []configuration.CatalogFile) error {
				if !confirming {
					return nil
				}
				if row.name == "expired" {
					now = now.Add(publication.GroupPreviewLifetime)
					return nil
				}
				_, _, err := access.Owner(ctx, owner, &externalaccess.Change{Version: policy.Version, Rotate: true})
				return err
			})
			service := externalaccess.NewTemplatePublications(groups, validator, clock)
			preview, err := service.Preview(ctx, permit, published.Settings.TemplateID, 1)
			if err != nil {
				t.Fatal(err)
			}
			confirming = true
			if result, err := service.Confirm(ctx, permit, published.Settings.TemplateID, preview.Receipt); result != nil || !errors.Is(err, row.want) {
				t.Fatalf("late writer %v, %v", result, err)
			}
			desired, err := NewPublications(db).ReadDesired(ctx, published.Settings.ID)
			if err != nil || desired.Number != 1 || notified != 0 {
				t.Fatalf("declined publish effects %v %v %d", desired, err, notified)
			}
		})
	}
}
