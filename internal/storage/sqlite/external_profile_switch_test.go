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

func TestExternalProfileSwitchFinalGuard(t *testing.T) {
	for _, scenario := range []string{"expired", "hidden"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			var late func() error
			validator := testPublicationValidator(func(context.Context, string, []configuration.CatalogFile) error {
				if late != nil {
					return late()
				}
				return nil
			})
			db, _, _, _ := switchSQLiteFixture(t, validator)
			if _, err := db.Exec("UPDATE external_access SET enabled=1,protected=0,version=2"); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			if _, err := db.Exec("INSERT INTO agent_contacts(router_id,contact_ns,applied,uncertain) VALUES(?,?,0,0)", switchRouterID, now.UnixNano()); err != nil {
				t.Fatal(err)
			}
			clock := func() time.Time { return now }
			permit, err := NewExternalAccess(db, clock).Admit(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			pubs := NewPublications(db)
			svc := externalaccess.NewProfileSwitches(NewExternalProfileSwitches(pubs), validator, clock)
			req := publication.ProfileSwitchRequest{RouterID: switchRouterID, TargetTemplateID: switchOffice}
			_, receipt, err := svc.Preview(ctx, permit, req)
			if err != nil {
				t.Fatal(err)
			}
			expected := publication.ErrConflict
			switch scenario {
			case "expired":
				late = func() error { now = now.Add(5 * time.Minute); return nil }
			case "hidden":
				expected = publication.ErrSourceMissing
				late = func() error { return NewRouterVisibility(db).Hide(ctx, switchRouterID) }
			}
			got, err := svc.Confirm(ctx, permit, req, receipt)
			if got != nil || !errors.Is(err, expected) {
				t.Fatalf("late change: %v, %v", got, err)
			}
			in, err := pubs.ReadProfileSwitch(ctx, switchRouterID, switchHome)
			if err != nil || !in.AlreadyActive || in.Basis.DesiredNumber != 1 {
				t.Fatal("state changed", err)
			}
		})
	}
}
