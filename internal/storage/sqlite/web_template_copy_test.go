package sqlite

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestWebTemplateCopyPreservesScopeAndExactDraft(t *testing.T) {
	db, _, digest, now := fixtureWebTemplateWriter(t)
	ctx := t.Context()
	storeCurrentPublication(t, db)
	service := templates.New(NewTemplateRepository(db))
	template, err := service.Get(ctx, "A")
	if err != nil {
		t.Fatal(err)
	}
	const uuid = "550e8400-e29b-41d4-a716-446655440000"
	template.Sections = []configuration.TemplateSection{{Name: "outbounds"}, {Name: "rules", Elements: []configuration.TemplateElement{
		{Disabled: true, Element: configuration.Element{ID: uuid, Content: " { \"x\": 9007199254740993, \"_xkeenUiId\": \"" + uuid + "\" }\r\n"}},
	}}}
	updated, err := service.Replace(ctx, template.TemplateDraft, template.Version)
	if err != nil {
		t.Fatal(err)
	}
	updated, err = service.Get(ctx, "A")
	if err != nil {
		t.Fatal(err)
	}
	copy := NewWebTemplates(db)
	if err := copy.Copy(ctx, digest, fixedWebTime(now), "A", "stale", 1); !errors.Is(err, templates.ErrVersionConflict) {
		t.Fatalf("stale source: %v", err)
	}
	const target = " next / # "
	if err := copy.Copy(ctx, digest, fixedWebTime(now), "A", target, updated.Version); err != nil {
		t.Fatal(err)
	}
	if err := copy.Copy(ctx, digest, fixedWebTime(now.Add(20*time.Minute)), "A", target, updated.Version); !errors.Is(err, templates.ErrConflict) {
		t.Fatalf("duplicate copy: %v", err)
	}
	if err := copy.Copy(ctx, digest, fixedWebTime(now.Add(webadmin.IdleLifetime)), "A", "expired", updated.Version); !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("failed copy extended session: %v", err)
	}
	saved, err := service.Get(ctx, target)
	if err != nil || saved == nil || saved.ID != target || saved.Version != 1 || !reflect.DeepEqual(saved.Sections, updated.Sections) {
		t.Fatalf("inexact copy: %+v %v", saved, err)
	}
	var originalRaw, copyRaw []byte
	if err := db.QueryRowContext(ctx, "SELECT (SELECT draft FROM templates WHERE id='A'), (SELECT draft FROM templates WHERE id=?)", target).Scan(&originalRaw, &copyRaw); err != nil || !bytes.Equal(originalRaw, copyRaw) {
		t.Fatalf("stored source was transformed: %v", err)
	}
	if got := readText(t, db, "SELECT template_id FROM active_router_publications WHERE router_id='router'"); got != "A" {
		t.Fatal("router assignment changed")
	}
	if got := readText(t, db, "SELECT count(*) FROM accepted_templates"); got != "1" {
		t.Fatal("copy published a template")
	}
	saved.Sections[1].Elements[0].Element.Content = `{}`
	if _, err := service.Replace(ctx, saved.TemplateDraft, 1); err != nil {
		t.Fatal(err)
	}
	original, err := service.Get(ctx, "A")
	if err != nil || !reflect.DeepEqual(original.Sections, updated.Sections) {
		t.Fatalf("editing copy changed source: %+v %v", original, err)
	}
}
