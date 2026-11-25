package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

const (
	switchRouterID  = "router.example"
	switchHome      = "home-template"
	switchOffice    = "office-template"
	switchThird     = "third-template"
	switchShared    = "550e8400-e29b-41d4-a716-446655440981"
	switchLocal     = "550e8400-e29b-41d4-a716-446655440982"
	switchOutbound  = "550e8400-e29b-41d4-a716-446655440983"
	switchInbound   = "550e8400-e29b-41d4-a716-446655440984"
	switchNewShared = "550e8400-e29b-41d4-a716-446655440985"
)

func switchTemplate(id, tag string) configuration.TemplateDraft {
	draft := configuration.TemplateDraft{ID: id, Sections: []configuration.TemplateSection{
		{Name: "outbounds", Elements: []configuration.TemplateElement{{Element: configuration.Element{
			ID: switchOutbound, Content: `{"protocol":"freedom","tag":"` + tag + `"}`,
		}}}},
	}}
	if id == switchHome {
		draft.Sections = append(draft.Sections, configuration.TemplateSection{Name: "inbounds", Elements: []configuration.TemplateElement{
			{Element: configuration.Element{ID: switchInbound, Content: `{"listen":"127.0.0.1","port":19899,"protocol":"socks","tag":"source"}`}},
		}}, configuration.TemplateSection{Name: "rules", Elements: []configuration.TemplateElement{
			{Element: configuration.Element{ID: switchShared, Content: `{"type":"field","network":"tcp","outboundTag":"direct"}`}},
		}})
	}
	return draft
}

func switchSQLiteFixture(t *testing.T, validator publication.CatalogValidator) (*sql.DB, string, routersettings.Draft, *publication.OrderBasis) {
	t.Helper()
	ctx := t.Context()
	db, path := openPublicationDB(t)
	templateStore := templates.New(NewTemplateRepository(db))
	for _, draft := range []configuration.TemplateDraft{switchTemplate(switchHome, "direct"), switchTemplate(switchOffice, "office"), switchTemplate(switchThird, "third")} {
		snapshot, err := templateStore.Create(ctx, draft)
		if err != nil {
			t.Fatal(err)
		}
		storeRawAccepted(t, db, snapshot)
	}
	draft := routersettings.Draft{ID: switchRouterID, Name: "Example router", TemplateID: switchHome,
		PersonalOrder: []string{switchLocal, switchShared}, DisabledInbounds: []string{switchInbound},
		Groups: []routersettings.Group{
			{Name: "rules", Local: []routersettings.LocalElement{{ID: switchLocal, Content: json.RawMessage(" {\r\n \"type\":\"field\",\"network\":\"udp\",\"outboundTag\":\"direct\"\n}\r\n")}},
				Exclusions: []configuration.ElementSource{{TemplateID: switchHome, ElementID: switchShared}}},
			{Name: "outbounds", Overrides: []configuration.ContentOverride{{
				Source: configuration.ElementSource{TemplateID: switchHome, ElementID: switchOutbound},
				Mode:   configuration.ContentModeReplacement, Content: json.RawMessage(" { \"protocol\": \"freedom\", \"tag\": \"direct\" } "),
				Values: map[string]json.RawMessage{"inactive": json.RawMessage(" \"remember\"\r\n")},
			}}},
		}}
	basis := &publication.OrderBasis{TemplateID: switchHome, TemplateVersion: 1, SharedRuleIDs: []string{switchShared}}
	result, err := publication.NewService(NewPublications(db), validator).Publish(ctx, publication.PublishRequest{
		Mode: publication.ConfirmAccepted, Source: publication.SourceCandidate, Candidate: &draft, OrderBasis: basis,
		Expected: publication.Expectations{RouterID: switchRouterID, TemplateID: switchHome, AcceptedVersion: 1},
	})
	requirePublicationOutcome(t, result, err, 1, true)
	if err := NewRouterSettings(db).Save(ctx, draft); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(basis)
	if _, err := db.ExecContext(ctx, "UPDATE router_settings SET order_basis=? WHERE id=?", raw, switchRouterID); err != nil {
		t.Fatal(err)
	}
	return db, path, draft, basis
}

func switchPreview(t *testing.T, service *publication.ProfileSwitchService, target string) (publication.ProfileSwitchRequest, *publication.ProfileSwitchPreview) {
	t.Helper()
	req := publication.ProfileSwitchRequest{RouterID: switchRouterID, TargetTemplateID: target}
	preview, err := service.Preview(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	return req, preview
}

func TestProfileSwitchSQLiteRoundTrip(t *testing.T) {
	checkProfileSwitchSQLiteRoundTrip(t, testPublicationValidator(func(context.Context, string, []configuration.CatalogFile) error { return nil }))
}

func checkProfileSwitchSQLiteRoundTrip(t *testing.T, validator publication.CatalogValidator) {
	t.Helper()
	ctx := t.Context()
	db, path, home, basis := switchSQLiteFixture(t, validator)
	store := NewPublications(db)
	service := publication.NewProfileSwitchService(store, validator)
	req, preview := switchPreview(t, service, switchOffice)
	if preview.Restoring || len(preview.PersonalOrder) != 0 || !preview.Changed {
		t.Fatalf("first office = %#v", preview)
	}
	before, err := store.Read(ctx, publication.ReadRequest{RouterID: switchRouterID, IncludeRouterDraft: true})
	if err != nil || before.TemplateID != switchHome || !reflect.DeepEqual(before.RouterDraft.Draft, home) {
		t.Fatalf("preview changed source: %#v %v", before, err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fixture_fail_switch BEFORE INSERT ON desired_router_revisions
		WHEN new.revision=2 BEGIN SELECT RAISE(FAIL,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if result, err := service.Confirm(ctx, req, preview.Proof); result != nil || err == nil {
		t.Fatalf("ignored failed transaction: %#v %v", result, err)
	}
	after, err := store.Read(ctx, publication.ReadRequest{RouterID: switchRouterID, IncludeRouterDraft: true})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("partial switch escaped rollback: %#v %v", after, err)
	}
	var saved int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM inactive_router_profile_drafts").Scan(&saved); err != nil || saved != 0 {
		t.Fatalf("rolled-back archival: %d %v", saved, err)
	}
	if _, err := db.ExecContext(ctx, "DROP TRIGGER fixture_fail_switch"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Confirm(ctx, req, preview.Proof)
	requirePublicationOutcome(t, result, err, 2, true)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store = NewPublications(db)
	service = publication.NewProfileSwitchService(store, validator)
	current, err := service.Read(ctx, switchRouterID, switchOffice)
	if err != nil || !current.AlreadyActive || current.Current.Draft.Name != home.Name || len(current.Current.Draft.Groups) != 0 {
		t.Fatalf("restart restored wrong office: %#v %v", current, err)
	}
	if _, err := service.Confirm(ctx, req, preview.Proof); !errors.Is(err, publication.ErrConflict) {
		t.Fatalf("replayed preview after restart: %v", err)
	}

	editor := templates.New(NewTemplateRepository(db))
	updated := switchTemplate(switchHome, "direct")
	updated.Sections[2].Elements = append(updated.Sections[2].Elements, configuration.TemplateElement{Element: configuration.Element{
		ID: switchNewShared, Content: `{"type":"field","network":"tcp","outboundTag":"direct"}`,
	}})
	latest, err := editor.Replace(ctx, updated, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = publication.NewTemplateGroupService(store, validator).Publish(ctx, publication.SharedPublishRequest{
		TemplateID: switchHome, Source: publication.SourceSharedDraft, TemplateDraftVersion: latest.Version, AcceptedVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	returned, err := service.Read(ctx, switchRouterID, switchHome)
	if err != nil || returned.TargetDraft == nil || !reflect.DeepEqual(returned.TargetDraft.Draft, home) ||
		!reflect.DeepEqual(returned.TargetDraft.OrderBasis, basis) {
		t.Fatalf("archived home changed: %#v %v", returned, err)
	}
	back, backPreview := switchPreview(t, service, switchHome)
	if !backPreview.Restoring || !reflect.DeepEqual(backPreview.PersonalOrder, []string{switchLocal, switchNewShared, switchShared}) {
		t.Fatalf("B08 did not restore and reconcile: %#v", backPreview)
	}
	result, err = service.Confirm(ctx, back, backPreview.Proof)
	requirePublicationOutcome(t, result, err, 3, true)
	final, err := store.Read(ctx, publication.ReadRequest{RouterID: switchRouterID, IncludeRouterDraft: true})
	if err != nil || final.TemplateID != switchHome || final.Active.Generation != before.Active.Generation+2 ||
		final.RouterDraft.Generation != before.RouterDraft.Generation+2 ||
		!reflect.DeepEqual(final.RouterDraft.Draft.Groups, home.Groups) ||
		!reflect.DeepEqual(final.RouterDraft.Draft.DisabledInbounds, home.DisabledInbounds) ||
		final.RouterDraft.OrderBasis.TemplateVersion != latest.Version {
		t.Fatalf("return lost exact values, mode, order or generations: %#v %v", final, err)
	}
	for _, file := range final.Desired.Catalog.Files {
		if bytes.Contains(file.Content, []byte(`"office"`)) {
			t.Fatal("office values remained in restored home")
		}
	}
}

type guardedProfileFixture struct {
	*Publications
	guard publicationGuard
}

func (s guardedProfileFixture) CommitProfileSwitch(ctx context.Context, plan publication.ProfileSwitchPlan) (int64, error) {
	return s.commitProfileSwitch(ctx, plan, s.guard)
}

func TestProfileSwitchLegacyScopeAndLateAuthority(t *testing.T) {
	validator := testPublicationValidator(func(context.Context, string, []configuration.CatalogFile) error { return nil })
	for _, legacy := range []string{switchOffice, switchThird} {
		t.Run(legacy, func(t *testing.T) {
			ctx := t.Context()
			db, path, home, _ := switchSQLiteFixture(t, validator)
			other, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			exact := json.RawMessage(" { \"protocol\": \"freedom\", \"tag\": \"local\" } \r\n")
			pending := routersettings.Draft{ID: switchRouterID, TemplateID: legacy, Name: "legacy pending",
				Groups: []routersettings.Group{{Name: "outbounds", Local: []routersettings.LocalElement{{ID: switchLocal, Content: exact}}}}}
			if err := NewRouterSettings(other).Save(ctx, pending); err != nil {
				t.Fatal(err)
			}
			store := NewPublications(db)
			service := publication.NewProfileSwitchService(store, validator)
			req, preview := switchPreview(t, service, switchOffice)
			if preview.Restoring != (legacy == switchOffice) {
				t.Fatalf("wrong origin for legacy slot: %#v", preview)
			}
			stale := publication.NewProfileSwitchService(store, testPublicationValidator(func(ctx context.Context, profile string, files []configuration.CatalogFile) error {
				pending.Name = "changed separately"
				return NewRouterSettings(other).Save(ctx, pending)
			}))
			if _, err := stale.Confirm(ctx, req, preview.Proof); !errors.Is(err, publication.ErrConflict) {
				t.Fatalf("late slot update was lost: %v", err)
			}
			req, preview = switchPreview(t, service, switchOffice)
			deny := publication.NewProfileSwitchService(guardedProfileFixture{store, func(context.Context, *sql.Tx) error {
				return publication.ErrEffectRefused
			}}, validator)
			if _, err := deny.Confirm(ctx, req, preview.Proof); !errors.Is(err, publication.ErrEffectRefused) {
				t.Fatalf("late revoked authority accepted: %v", err)
			}
			result, err := service.Confirm(ctx, req, preview.Proof)
			requirePublicationOutcome(t, result, err, 2, true)
			recoverHome, err := service.Read(ctx, switchRouterID, switchHome)
			if err != nil || recoverHome.TargetDraft == nil || !reflect.DeepEqual(recoverHome.TargetDraft.Draft, home) {
				t.Fatalf("source C06 fallback lost: %#v %v", recoverHome, err)
			}
			if legacy == switchThird {
				recoverThird, err := service.Read(ctx, switchRouterID, switchThird)
				if err != nil || recoverThird.TargetDraft == nil || !reflect.DeepEqual(recoverThird.TargetDraft.Draft, pending) {
					t.Fatalf("independent third scope lost: %#v %v", recoverThird, err)
				}
			} else {
				activeOffice, err := service.Read(ctx, switchRouterID, switchOffice)
				if err != nil || !activeOffice.AlreadyActive || !reflect.DeepEqual(activeOffice.Current.Draft.Groups, pending.Groups) {
					t.Fatalf("legacy target content not restored: %#v %v", activeOffice, err)
				}
			}
		})
	}
}

func TestProfileSwitchSharesMembershipCASWithC07(t *testing.T) {
	ctx := t.Context()
	validator := testPublicationValidator(func(context.Context, string, []configuration.CatalogFile) error { return nil })
	db, path, _, _ := switchSQLiteFixture(t, validator)
	other, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	store := NewPublications(db)
	group := NewPublications(other)
	service := publication.NewProfileSwitchService(store, validator)
	request, preview := switchPreview(t, service, switchOffice)
	updated := switchTemplate(switchHome, "direct")
	updated.Sections = append(updated.Sections, configuration.TemplateSection{Name: "stats"})
	shared := publication.SharedPublishRequest{
		TemplateID: switchHome, Source: publication.SourceSharedCandidate, Candidate: &updated,
		TemplateDraftVersion: 1, AcceptedVersion: 1,
	}
	duringConfirm := publication.NewProfileSwitchService(store, testPublicationValidator(func(ctx context.Context, _ string, _ []configuration.CatalogFile) error {
		outcome, err := publication.NewTemplateGroupService(group, validator).Publish(ctx, shared)
		if err != nil || outcome.Version != 2 || len(outcome.Routers) != 1 {
			t.Fatalf("concurrent C07 = %#v %v", outcome, err)
		}
		return nil
	}))
	if _, err := duringConfirm.Confirm(ctx, request, preview.Proof); !errors.Is(err, publication.ErrConflict) {
		t.Fatalf("stale switch after C07: %v", err)
	}
	current, err := service.Read(ctx, switchRouterID, switchHome)
	if err != nil || !current.AlreadyActive || current.Basis.SourceAcceptedVersion != 2 {
		t.Fatalf("C07 or stale switch corrupted assignment: %#v %v", current, err)
	}

	request, preview = switchPreview(t, service, switchOffice)
	shared.TemplateDraftVersion, shared.AcceptedVersion = 2, 2
	overlappingGroup := publication.NewTemplateGroupService(group, testPublicationValidator(func(ctx context.Context, _ string, _ []configuration.CatalogFile) error {
		result, err := service.Confirm(ctx, request, preview.Proof)
		if err != nil || result == nil {
			t.Fatalf("independent switch failed: %#v %v", result, err)
		}
		return nil
	}))
	if _, err := overlappingGroup.Publish(ctx, shared); !errors.Is(err, publication.ErrConflict) {
		t.Fatalf("C07 reused the outdated membership: %v", err)
	}
	current, err = service.Read(ctx, switchRouterID, switchOffice)
	if err != nil || !current.AlreadyActive {
		t.Fatalf("late group replaced new assignment: %#v %v", current, err)
	}
}
