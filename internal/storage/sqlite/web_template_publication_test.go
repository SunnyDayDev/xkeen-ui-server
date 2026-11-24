package sqlite

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

func TestWebTemplatePublicationReceiptIsBoundAndExpires(t *testing.T) {
	ctx := t.Context()
	db, _, _, now := fixtureWebTemplateWriter(t)
	admin := NewWebAdmin(db)
	token := base64.RawURLEncoding.EncodeToString(make([]byte, webadmin.TokenBytes))
	rawOther := make([]byte, webadmin.TokenBytes)
	rawOther[0] = 1
	other := base64.RawURLEncoding.EncodeToString(rawOther)
	admit := func(raw string) string {
		t.Helper()
		digest, csrf, err := webadmin.SessionProof(raw, "", false)
		if err != nil {
			t.Fatal(err)
		}
		if err := admin.Issue(ctx, 1, digest, [32]byte{}, now); err != nil {
			t.Fatal(err)
		}
		return csrf
	}
	csrf, otherCSRF := admit(token), admit(other)
	draft := configuration.TemplateDraft{ID: "template-exact"}
	if _, err := templates.New(NewTemplateRepository(db)).Create(ctx, draft); err != nil {
		t.Fatal(err)
	}
	store := NewWebTemplatePublications(NewPublications(db))
	clock := func() time.Time { return now }
	service := webadmin.NewTemplatePublication(store, nil, clock)
	plan, err := service.Preview(ctx, token, draft.ID, 1)
	if err != nil || plan == nil || plan.Receipt == "" || len(plan.Preview.Members) != 0 {
		t.Fatalf("dry run: %+v %v", plan, err)
	}
	if got := readText(t, db, "SELECT count(*) FROM accepted_templates"); got != "0" {
		t.Fatal("preview published the source")
	}
	for _, bad := range []struct{ token, csrf, id, receipt string }{
		{other, otherCSRF, draft.ID, plan.Receipt},
		{token, csrf, "other", plan.Receipt},
		{token, csrf, draft.ID, strings.Replace(plan.Receipt, ".", "..", 1)},
	} {
		if result, err := service.Confirm(ctx, bad.token, bad.csrf, bad.id, bad.receipt); result != nil || !errors.Is(err, webadmin.ErrTemplatePreviewChanged) {
			t.Fatalf("invalid receipt %+v %v", result, err)
		}
	}
	now = now.Add(5 * time.Minute)
	if result, err := service.Confirm(ctx, token, csrf, draft.ID, plan.Receipt); result != nil || !errors.Is(err, webadmin.ErrTemplatePreviewChanged) {
		t.Fatalf("expired receipt %+v %v", result, err)
	}
	refreshed, err := service.Preview(ctx, token, draft.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := service.Confirm(ctx, token, csrf, draft.ID, refreshed.Receipt)
	if err != nil || confirmed.Version != 1 || len(confirmed.Routers) != 0 {
		t.Fatalf("confirmed %+v %v", confirmed, err)
	}
}

func TestWebTemplatePublicationIncludesHiddenOfflineActiveAssignment(t *testing.T) {
	ctx := t.Context()
	db, _, _, now := fixtureWebTemplateWriter(t)
	storeCurrentPublication(t, db)
	clock := func() time.Time { return now }
	credentials := agentcredentials.NewService(NewAgentCredentials(db), clock)
	invite, err := credentials.Issue(ctx, "router", agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	id, err := credentials.Enroll(ctx, invite, agentcredential.Generate(agentcredential.Working).Encode())
	if err != nil {
		t.Fatal(err)
	}
	if err := NewContacts(db, clock).Accept(ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	now = now.Add(180 * time.Second)
	if err := NewRouterVisibility(db).Hide(ctx, id.RouterID); err != nil {
		t.Fatal(err)
	}
	if _, err := templates.New(NewTemplateRepository(db)).Create(ctx, configuration.TemplateDraft{ID: "B"}); err != nil {
		t.Fatal(err)
	}
	if err := NewRouterSettings(db).Save(ctx, routersettings.Draft{ID: id.RouterID, TemplateID: "B"}); err != nil {
		t.Fatal(err)
	}

	token := base64.RawURLEncoding.EncodeToString(make([]byte, webadmin.TokenBytes))
	digest, csrf, err := webadmin.SessionProof(token, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewWebAdmin(db).Issue(ctx, 1, digest, [32]byte{}, now); err != nil {
		t.Fatal(err)
	}
	expireDuringVerification := false
	validator := testPublicationValidator(func(context.Context, string, []configuration.CatalogFile) error {
		if expireDuringVerification {
			now = now.Add(5 * time.Minute)
		}
		return nil
	})
	service := webadmin.NewTemplatePublication(NewWebTemplatePublications(NewPublications(db)), validator, clock)
	plan, err := service.Preview(ctx, token, "A", 1)
	if err != nil || len(plan.Preview.Members) != 1 || plan.Preview.Members[0].Expected.RouterID != id.RouterID ||
		len(plan.Contact) != 1 || plan.Contact[0] != reporting.Offline {
		t.Fatalf("active group lost hidden/offline member: %+v %v", plan, err)
	}
	staged, err := service.Preview(ctx, token, "B", 1)
	if err != nil || len(staged.Preview.Members) != 0 {
		t.Fatalf("pending reassignment contaminated membership: %+v %v", staged, err)
	}
	expireDuringVerification = true
	if result, err := service.Confirm(ctx, token, csrf, "A", plan.Receipt); result != nil || !errors.Is(err, webadmin.ErrTemplatePreviewChanged) {
		t.Fatalf("expiry in writer ignored: %+v %v", result, err)
	}
	if got := readText(t, db, "SELECT max(revision) FROM desired_router_revisions WHERE router_id='router'"); got != "1" {
		t.Fatal("expired receipt published files")
	}
}
