package routersettings_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

const (
	localID  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	sharedID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

type memoryRepository map[string]routersettings.Draft

func (m memoryRepository) Save(_ context.Context, draft routersettings.Draft) error {
	m[draft.ID] = draft
	return nil
}

func (m memoryRepository) Get(_ context.Context, id string) (routersettings.Draft, bool, error) {
	draft, found := m[id]
	return draft, found, nil
}

func baseline() routersettings.Draft {
	return routersettings.Draft{
		ID: "router.example", Name: "Устройство", TemplateID: "template.example",
		Groups: []routersettings.Group{{
			Name:  "rules",
			Local: []routersettings.LocalElement{{ID: localID, Content: json.RawMessage("null")}},
			Overrides: []configuration.ContentOverride{{
				Source: configuration.ElementSource{TemplateID: "template.example", ElementID: sharedID},
				Mode:   configuration.ContentModeReplacement, Content: json.RawMessage("null"),
				Values: map[string]json.RawMessage{"missing.now": json.RawMessage("null")},
			}},
		}},
		PersonalOrder: []string{sharedID, localID},
	}
}

func TestDraftServiceStoresAndReadsByExactID(t *testing.T) {
	ctx := context.Background()
	service := routersettings.NewService(memoryRepository{})
	first := baseline()
	second := baseline()
	second.ID = "Router.example"
	second.Name = "Second router"
	for _, draft := range []routersettings.Draft{first, second} {
		if err := service.Save(ctx, draft); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []routersettings.Draft{first, second} {
		got, found, err := service.Get(ctx, want.ID)
		if err != nil || !found || got.Name != want.Name || got.TemplateID != want.TemplateID {
			t.Fatalf("Get(%q): found %v, error %v, got %+v", want.ID, found, err, got)
		}
	}
	if _, found, err := service.Get(ctx, "missing.example"); found || err != nil {
		t.Fatalf("unknown router: found %v, error %v", found, err)
	}
}

func TestDraftServiceChecksOnlySnapshotStructure(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		mutate func(*routersettings.Draft)
	}{
		{"empty ID", func(d *routersettings.Draft) { d.ID = "" }},
		{"invalid ID", func(d *routersettings.Draft) { d.ID = "\xff" }},
		{"no template", func(d *routersettings.Draft) { d.TemplateID = "" }},
		{"duplicate section", func(d *routersettings.Draft) {
			d.Groups = append(d.Groups, routersettings.Group{Name: "rules"})
		}},
		{"duplicate local UUID across sections", func(d *routersettings.Draft) {
			d.Groups = append(d.Groups, routersettings.Group{Name: "inbounds", Local: d.Groups[0].Local})
		}},
		{"wrong template", func(d *routersettings.Draft) { d.Groups[0].Overrides[0].Source.TemplateID = "another.example" }},
		{"duplicate override", func(d *routersettings.Draft) {
			d.Groups = append(d.Groups, routersettings.Group{Name: "inbounds", Overrides: d.Groups[0].Overrides})
		}},
		{"duplicate exclusions", func(d *routersettings.Draft) {
			ref := d.Groups[0].Overrides[0].Source
			d.Groups[0].Exclusions = []configuration.ElementSource{ref, ref}
		}},
		{"replacement missing JSON", func(d *routersettings.Draft) { d.Groups[0].Overrides[0].Content = nil }},
		{"variables with own JSON", func(d *routersettings.Draft) { d.Groups[0].Overrides[0].Mode = configuration.ContentModeVariables }},
		{"local missing JSON", func(d *routersettings.Draft) { d.Groups[0].Local[0].Content = nil }},
		{"invalid local JSON", func(d *routersettings.Draft) {
			d.Groups[0].Local[0].Content = json.RawMessage(`{"private-fixture":1,"private-fixture":2}`)
		}},
		{"invalid variable JSON", func(d *routersettings.Draft) {
			d.Groups[0].Overrides[0].Values["private-fixture"] = json.RawMessage("broken")
		}},
		{"duplicate order UUID", func(d *routersettings.Draft) { d.PersonalOrder = []string{localID, localID} }},
		{"duplicate disabled UUID", func(d *routersettings.Draft) { d.DisabledRules = []string{localID, localID} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := memoryRepository{}
			service := routersettings.NewService(repository)
			previous := baseline()
			if err := service.Save(ctx, previous); err != nil {
				t.Fatal(err)
			}
			candidate := baseline()
			candidate.Name = "Invalid draft"
			tt.mutate(&candidate)
			err := service.Save(ctx, candidate)
			if !errors.Is(err, routersettings.ErrInvalidDraft) {
				t.Fatalf("expected invalid draft, got %v", err)
			}
			if strings.Contains(err.Error(), "private-fixture") {
				t.Fatal("diagnostic contains a JSON key or variable name")
			}
			if len(repository) != 1 || repository[previous.ID].Name != previous.Name {
				t.Fatal("invalid draft changed the repository")
			}
		})
	}
}
