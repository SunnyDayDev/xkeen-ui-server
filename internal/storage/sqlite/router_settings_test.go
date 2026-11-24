package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func routerFixture() routersettings.Draft {
	const (
		template = "template.example"
		shared   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		local    = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		inbound  = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	)
	source := configuration.ElementSource{TemplateID: template, ElementID: shared}
	return routersettings.Draft{
		ID: "router.example", Name: "Устройство", TemplateID: template,
		Groups: []routersettings.Group{
			{
				Name: "rules",
				Local: []routersettings.LocalElement{{
					ID: local, Content: json.RawMessage("  { \"precise\": 9007199254740993, \"var\": \"$${keep}\" }\n"),
				}},
				Overrides: []configuration.ContentOverride{{
					Source: source, Mode: configuration.ContentModeReplacement, Content: json.RawMessage(" null\n"),
					Values: map[string]json.RawMessage{"a.b": json.RawMessage(" [ null, 1.2300e+4 ] ")},
				}},
				Exclusions: []configuration.ElementSource{source},
			},
			{Name: "inbounds", Local: []routersettings.LocalElement{{ID: inbound, Content: json.RawMessage("null")}}},
			{Name: "customGroup"},
		},
		PersonalOrder: []string{shared, local},
		DisabledRules: []string{local}, DisabledInbounds: []string{inbound},
	}
}

func TestRouterSettingsRoundTripAfterReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "routers.sqlite")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := routersettings.NewService(NewRouterSettings(db))
	first := routerFixture()
	inherited := routersettings.Draft{ID: "Router.example", Name: "Other", TemplateID: first.TemplateID}
	explicit := routersettings.Draft{ID: "router-empty.example", TemplateID: first.TemplateID, PersonalOrder: []string{}}
	for _, draft := range []routersettings.Draft{first, inherited, explicit} {
		if err := service.Save(ctx, draft); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service = routersettings.NewService(NewRouterSettings(db))
	for _, want := range []routersettings.Draft{first, inherited, explicit} {
		got := readRouter(t, service, want.ID)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("snapshot did not round-trip for %q:\n got %+v\nwant %+v", want.ID, got, want)
		}
	}
	got := readRouter(t, service, first.ID)
	got.Groups[0].Local[0].Content[0] = 'X'
	got.Groups[0].Overrides[0].Values["a.b"][0] = 'X'
	got.PersonalOrder[0] = "changed"
	if got := readRouter(t, service, first.ID); !reflect.DeepEqual(got, first) {
		t.Fatal("mutating the read result changed stored state")
	}
	if _, found, err := service.Get(ctx, "missing.example"); found || err != nil {
		t.Fatalf("unknown router: found=%v err=%v", found, err)
	}
}

func readRouter(t *testing.T, service *routersettings.Service, id string) routersettings.Draft {
	t.Helper()
	got, found, err := service.Get(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("Get(%q): found %v, error %v", id, found, err)
	}
	return got
}

func TestRouterSettingsCorruptSnapshotDoesNotLookAbsent(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "routers.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	draft := routerFixture()
	service := routersettings.NewService(NewRouterSettings(db))
	if err := service.Save(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE router_settings SET snapshot = ? WHERE id = ?", []byte(`{"private-fixture":`), draft.ID); err != nil {
		t.Fatal(err)
	}
	got, found, err := service.Get(ctx, draft.ID)
	if found || got.ID != "" || !errors.Is(err, routersettings.ErrStoredDraft) || strings.Contains(err.Error(), "private-fixture") {
		t.Fatalf("damaged router: found=%v got=%+v err=%v", found, got, err)
	}
}

func TestRouterSettingsFailedWritePreservesSnapshots(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "routers.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	draft := routerFixture()
	other := routersettings.Draft{ID: "other.example", TemplateID: draft.TemplateID}
	service := routersettings.NewService(NewRouterSettings(db))
	for _, fixture := range []routersettings.Draft{draft, other} {
		if err := service.Save(ctx, fixture); err != nil {
			t.Fatal(err)
		}
	}
	execSQL(t, db, `CREATE TRIGGER reject_update BEFORE UPDATE ON router_settings
		BEGIN SELECT RAISE(ABORT, 'private-fixture'); END`)
	next := draft
	next.Name = "Next draft"
	next.Groups = nil
	if err := service.Save(ctx, next); err == nil || strings.Contains(err.Error(), "private-fixture") {
		t.Fatalf("failed write should yield a safe error, got %v", err)
	}
	for _, want := range []routersettings.Draft{draft, other} {
		if got := readRouter(t, service, want.ID); !reflect.DeepEqual(got, want) {
			t.Fatalf("failed write changed %q", want.ID)
		}
	}
	execSQL(t, db, "DROP TRIGGER reject_update")
	if err := service.Save(ctx, next); err != nil {
		t.Fatal(err)
	}
	if got := readRouter(t, service, next.ID); !reflect.DeepEqual(got, next) {
		t.Fatal("successful retry did not replace whole snapshot")
	}
}
