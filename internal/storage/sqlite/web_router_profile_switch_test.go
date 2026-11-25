package sqlite

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestWebProfileSwitchRechecksAdmissionAfterValidation(t *testing.T) {
	for _, change := range []string{"reset", "expiry", "hide"} {
		t.Run(change, func(t *testing.T) {
			ctx := t.Context()
			verify := testPublicationValidator(func(context.Context, string, []configuration.CatalogFile) error { return nil })
			db, _, _, _ := switchSQLiteFixture(t, verify)
			owner := NewWebAdmin(db)
			now := time.Unix(1000000000, 0)
			token := base64.RawURLEncoding.EncodeToString(make([]byte, webadmin.TokenBytes))
			digest, csrf, err := webadmin.SessionProof(token, "", false)
			if err != nil {
				t.Fatal(err)
			}
			if err := owner.SaveVerifier(ctx, webadmin.Verifier{Version: 1}, false); err != nil {
				t.Fatal(err)
			}
			if err := owner.Issue(ctx, 1, digest, [32]byte{}, now); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, "INSERT INTO agent_contacts(router_id,contact_ns,report,applied,uncertain) VALUES (?,?,NULL,0,0)", switchRouterID, now.UnixNano()); err != nil {
				t.Fatal(err)
			}

			var beforeWrite func()
			service := webadmin.NewRouterProfileSwitch(NewWebRouterProfiles(NewPublications(db)),
				testPublicationValidator(func(context.Context, string, []configuration.CatalogFile) error {
					if beforeWrite != nil {
						beforeWrite()
					}
					return nil
				}), func() time.Time { return now })
			req := publication.ProfileSwitchRequest{RouterID: switchRouterID, TargetTemplateID: switchOffice}
			preview, err := service.Preview(ctx, token, csrf, req)
			if err != nil {
				t.Fatal(err)
			}
			want := publication.ErrConflict
			if change == "reset" {
				want = webadmin.ErrSignedOut
			}
			beforeWrite = func() {
				switch change {
				case "expiry":
					now = now.Add(5 * time.Minute)
				case "reset":
					if err := owner.SaveVerifier(ctx, webadmin.Verifier{Version: 1}, true); err != nil {
						t.Fatal(err)
					}
				case "hide":
					if err := NewRouterVisibility(db).Hide(ctx, switchRouterID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if result, err := service.Confirm(ctx, token, csrf, req, preview.Receipt); result != nil || !errors.Is(err, want) {
				t.Fatalf("changed admission allowed confirmation: %+v %v", result, err)
			}
			active, err := NewPublications(db).ReadProfileSwitch(ctx, switchRouterID, switchHome)
			if err != nil || !active.AlreadyActive || active.Desired.Number != 1 {
				t.Fatalf("rejected switch changed assignment: %+v %v", active, err)
			}
		})
	}
}
