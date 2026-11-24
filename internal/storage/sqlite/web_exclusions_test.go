package sqlite

import (
	"context"
	"errors"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"testing"
)

func TestWebExclusionDraftCAS(t *testing.T) {
	db, store, session, now := fixtureWebTemplateWriter(t)
	ctx := t.Context()
	_, err := db.ExecContext(ctx, "INSERT INTO templates(id,draft) VALUES('example','[]')")
	if err != nil {
		t.Fatal(err)
	}
	read := func() webadmin.ExclusionDraft {
		t.Helper()
		s, err := store.ReadExclusions(ctx, session, fixedWebTime(now), "example")
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if read().Version != 0 {
		t.Fatal("configured before create")
	}
	v := "2001:db8::/32"
	edit := webadmin.ExclusionDraft{Text: "# example\r\n${net}\r\n", Variables: []webadmin.ExclusionVariable{{Name: "net", Default: &v}}}
	saved, err := store.SaveExclusions(ctx, session, fixedWebTime(now), "example", edit)
	if err != nil || saved.Version != 1 || saved.ID == "" {
		t.Fatal(saved, err)
	}
	if _, err := store.SaveExclusions(ctx, session, fixedWebTime(now), "example", edit); !errors.Is(err, templates.ErrVersionConflict) {
		t.Fatal("stale", err)
	}
	path := readText(t, db, "SELECT file FROM pragma_database_list WHERE name='main'")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	store = NewWebTemplates(reopened)
	actual := read()
	if actual.Text != edit.Text || actual.ID != saved.ID || *actual.Variables[0].Default != v {
		t.Fatal("source lost")
	}
	actual.Text = ""
	actual.Variables = nil
	clear, err := store.SaveExclusions(ctx, session, fixedWebTime(now), "example", actual)
	if err != nil || clear.ID != saved.ID || clear.Version != 2 {
		t.Fatal(clear, err)
	}
	if _, err := store.ReadExclusions(ctx, session, fixedWebTime(now), "other"); !errors.Is(err, templates.ErrNotFound) {
		t.Fatal("unknown parent", err)
	}
	if _, err := store.SaveExclusions(ctx, [32]byte{}, fixedWebTime(now), "example", clear); !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatal("session", err)
	}
}

func TestExclusionPublicationRefusesUnsupportedBypass(t *testing.T) {
	db, web, session, now := fixtureWebTemplateWriter(t)
	ctx := t.Context()
	if _, err := templates.New(NewTemplateRepository(db)).Create(ctx, configuration.TemplateDraft{ID: "edge"}); err != nil {
		t.Fatal(err)
	}
	d, err := web.SaveExclusions(ctx, session, fixedWebTime(now), "edge", webadmin.ExclusionDraft{Text: "192.0.2.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewPublications(db)
	validator := testPublicationValidator(func(context.Context, string, []configuration.CatalogFile) error { return nil })
	group := publication.NewTemplateGroupService(repo, validator)
	version := int64(2)
	shared := publication.SharedPublishRequest{TemplateID: "edge", Source: publication.SourceSharedDraft, TemplateDraftVersion: version}
	if _, err = group.Publish(ctx, shared); err != nil {
		t.Fatal("zero members", err)
	}
	single := publication.NewService(repo, validator)
	router := routersettings.Draft{ID: "router.example", TemplateID: "edge"}
	req := publication.PublishRequest{Mode: publication.ConfirmAccepted, Source: publication.SourceCandidate, Candidate: &router, Expected: publication.Expectations{RouterID: router.ID, TemplateID: "edge", AcceptedVersion: version}}
	d.Text = ""
	d, err = web.SaveExclusions(ctx, session, fixedWebTime(now), "edge", d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = single.Publish(ctx, req); !errors.Is(err, publication.ErrBypassUnavailable) {
		t.Fatal("accepted nonempty overwritten by unpublished empty", err)
	}
	shared.AcceptedVersion = version
	shared.TemplateDraftVersion++
	if _, err = group.Publish(ctx, shared); err != nil {
		t.Fatal(err)
	}
	req.Expected.AcceptedVersion = shared.TemplateDraftVersion
	if _, err = single.Publish(ctx, req); err != nil {
		t.Fatal("empty source", err)
	}
	d.Text = "2001:db8::/32"
	if _, err = web.SaveExclusions(ctx, session, fixedWebTime(now), "edge", d); err != nil {
		t.Fatal(err)
	}
	shared.AcceptedVersion = shared.TemplateDraftVersion
	shared.TemplateDraftVersion++
	if _, err = group.Publish(ctx, shared); !errors.Is(err, publication.ErrBypassUnavailable) {
		t.Fatal("group did not block", err)
	}
	if readText(t, db, "SELECT max(version) FROM accepted_templates") != "3" || readText(t, db, "SELECT max(revision) FROM desired_router_revisions") != "1" {
		t.Fatal("partial group effects")
	}
}

func TestCopySharedExclusionsKeepsScopedIdentity(t *testing.T) {
	db, store, session, now := fixtureWebTemplateWriter(t)
	ctx := t.Context()
	if _, err := templates.New(NewTemplateRepository(db)).Create(ctx, configuration.TemplateDraft{ID: "source"}); err != nil {
		t.Fatal(err)
	}
	s, err := store.SaveExclusions(ctx, session, fixedWebTime(now), "source", webadmin.ExclusionDraft{Text: "192.0.2.1\n"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Copy(ctx, session, fixedWebTime(now), "source", "target", 2); err != nil {
		t.Fatal(err)
	}
	copied, err := store.ReadExclusions(ctx, session, fixedWebTime(now), "target")
	if err != nil || copied.Version != 1 || copied.ID != s.ID || copied.Text != s.Text {
		t.Fatal("copy omitted shared source", err)
	}
	copied.Text = ""
	if _, err := store.SaveExclusions(ctx, session, fixedWebTime(now), "target", copied); err != nil {
		t.Fatal(err)
	}
	original, err := store.ReadExclusions(ctx, session, fixedWebTime(now), "source")
	if err != nil || original.Text != s.Text {
		t.Fatal("copy aliases original", err)
	}
	if readText(t, db, "SELECT count(*) FROM accepted_xkeen_exclusions") != "0" {
		t.Fatal("copy published")
	}
}
