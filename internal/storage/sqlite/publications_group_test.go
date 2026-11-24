package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func storeGroupPublication(t *testing.T, db *sql.DB) {
	t.Helper()
	storeCurrentPublication(t, db)
	template, err := NewTemplateRepository(db).Find(t.Context(), "A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPublications(db).Commit(t.Context(), firstPublication("second", template, false)); err != nil {
		t.Fatal(err)
	}
}

func TestPublicationGroupReadUsesOnlyConfirmedAssignments(t *testing.T) {
	db, _ := openPublicationDB(t)
	storeGroupPublication(t, db)
	saveTemplateDraft(t, db, "B")
	pending := routersettings.Draft{ID: "router", TemplateID: "B", Name: "pending"}
	if err := routersettings.NewService(NewRouterSettings(db)).Save(t.Context(), pending); err != nil {
		t.Fatal(err)
	}
	s := NewPublications(db)
	a, err := s.ReadTemplateGroup(t.Context(), "A")
	if err != nil {
		t.Fatal(err)
	}
	if a.TemplateDraft.ID != "A" || a.Accepted.Version != 1 || len(a.Members) != 2 {
		t.Fatalf("group A = %#v", a)
	}
	var ids []string
	for _, member := range a.Members {
		ids = append(ids, member.Active.Draft.ID)
		if member.Active.Draft.TemplateID != "A" || member.Desired.Number != 1 {
			t.Fatalf("wrong active or desired inputs: %#v", member)
		}
	}
	if !reflect.DeepEqual(ids, []string{"router", "second"}) || a.Members[0].Active.Draft.Name != "old" {
		t.Fatalf("read a pending assignment or wrong order: %#v", a)
	}

	b, err := s.ReadTemplateGroup(t.Context(), "B")
	if err != nil || b.TemplateDraft.ID != "B" || b.Accepted != nil || len(b.Members) != 0 {
		t.Fatalf("group B = %#v, %v", b, err)
	}
	if got, err := s.ReadTemplateGroup(t.Context(), "missing"); got != nil || !errors.Is(err, publication.ErrSourceMissing) {
		t.Fatalf("missing template = %#v, %v", got, err)
	}
	execSQL(t, db, "UPDATE active_router_publications SET settings=x'6e756c6c' WHERE router_id='second'")
	if got, err := s.ReadTemplateGroup(t.Context(), "A"); got != nil || !errors.Is(err, publication.ErrStoredState) {
		t.Fatalf("partial group on corruption = %#v, %v", got, err)
	}
}

func groupPlanFromRead(t *testing.T, db *sql.DB, templateID string) publication.TemplateGroupPlan {
	t.Helper()
	inputs, err := NewPublications(db).ReadTemplateGroup(t.Context(), templateID)
	if err != nil {
		t.Fatal(err)
	}
	plan := publication.TemplateGroupPlan{TemplateID: templateID, TemplateDraftVersion: inputs.TemplateDraft.Version}
	if inputs.Accepted != nil {
		plan.AcceptedVersion = inputs.Accepted.Version
	}
	for _, member := range inputs.Members {
		plan.Members = append(plan.Members, publication.GroupRouterPlan{
			Expected: publication.GroupRouterExpectation{RouterID: member.Active.Draft.ID,
				ActiveGeneration: member.Active.Generation, DesiredNumber: member.Desired.Number},
			PersonalOrder: member.Active.Draft.PersonalOrder, OrderBasis: member.Active.OrderBasis,
		})
	}
	return plan
}

func TestPublicationGroupRejectsIncompletePlansAndStaleTokens(t *testing.T) {
	db, path := openPublicationDB(t)
	storeGroupPublication(t, db)
	s := NewPublications(db)
	for _, test := range []struct {
		name   string
		mutate func(*publication.TemplateGroupPlan)
		want   error
	}{
		{"missing member", func(plan *publication.TemplateGroupPlan) { plan.Members = plan.Members[:1] }, publication.ErrConflict},
		{"duplicate member", func(plan *publication.TemplateGroupPlan) { plan.Members[1] = plan.Members[0] }, publication.ErrInvalidState},
		{"foreign member", func(plan *publication.TemplateGroupPlan) { plan.Members[1].Expected.RouterID = "foreign" }, publication.ErrConflict},
		{"active token", func(plan *publication.TemplateGroupPlan) { plan.Members[1].Expected.ActiveGeneration++ }, publication.ErrConflict},
		{"desired token", func(plan *publication.TemplateGroupPlan) { plan.Members[1].Expected.DesiredNumber++ }, publication.ErrConflict},
		{"accepted token", func(plan *publication.TemplateGroupPlan) { plan.AcceptedVersion = 0 }, publication.ErrConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := groupPlanFromRead(t, db, "A")
			test.mutate(&plan)
			if result, err := s.CommitTemplateGroup(t.Context(), plan); result != nil || !errors.Is(err, test.want) {
				t.Fatalf("commit = %#v, %v; want %v", result, err, test.want)
			}
		})
	}
	plan := groupPlanFromRead(t, db, "A")
	other, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	repo := templates.New(NewTemplateRepository(other))
	draft, err := repo.Get(t.Context(), "A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Replace(t.Context(), draft.TemplateDraft, draft.Version); err != nil {
		t.Fatal(err)
	}
	if result, err := s.CommitTemplateGroup(t.Context(), plan); result != nil || !errors.Is(err, publication.ErrConflict) {
		t.Fatalf("used common draft changed = %#v, %v", result, err)
	}
}

func TestPublicationGroupDetectsPhantomMembership(t *testing.T) {
	for _, change := range []string{"new assignment", "removed", "reassigned", "first from empty"} {
		t.Run(change, func(t *testing.T) {
			db, path := openPublicationDB(t)
			if change == "first from empty" {
				storeRawAccepted(t, db, saveTemplateDraft(t, db, "A"))
			} else {
				storeGroupPublication(t, db)
			}
			plan := groupPlanFromRead(t, db, "A")
			other, err := Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			switch change {
			case "new assignment", "first from empty":
				template, err := NewTemplateRepository(other).Find(t.Context(), "A")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := NewPublications(other).Commit(t.Context(), firstPublication("new", template, false)); err != nil {
					t.Fatal(err)
				}
			case "removed":
				execSQL(t, other, "DELETE FROM active_router_publications WHERE router_id='second'")
			case "reassigned":
				execSQL(t, other, "UPDATE active_router_publications SET template_id='B' WHERE router_id='second'")
			}
			if result, err := NewPublications(db).CommitTemplateGroup(t.Context(), plan); result != nil || !errors.Is(err, publication.ErrConflict) {
				t.Fatalf("phantom commit = %#v, %v", result, err)
			}
		})
	}
}

func groupFiles(marker string) *publication.VerifiedFiles {
	return &publication.VerifiedFiles{Profile: configuration.XrayIdentityVersion, Files: []configuration.CatalogFile{
		{Name: "01_log.json", Content: []byte(marker)},
	}}
}

func TestPublicationGroupCommitsDraftOrderAndOnlyChangedRevisions(t *testing.T) {
	db, path := openPublicationDB(t)
	storeGroupPublication(t, db)
	active := routersettings.Draft{ID: "second", TemplateID: "A", Name: "keep me", PersonalOrder: []string{}}
	basis := publication.OrderBasis{TemplateID: "A", TemplateVersion: 1, SharedRuleIDs: []string{}}
	raw, _ := json.Marshal(encodeRouterDraft(active))
	rawBasis, _ := json.Marshal(basis)
	if _, err := db.Exec("UPDATE active_router_publications SET settings=?,order_basis=? WHERE router_id=?", raw, rawBasis, active.ID); err != nil {
		t.Fatal(err)
	}
	pending := routersettings.Draft{ID: "second", TemplateID: "B", Name: "pending"}
	if err := routersettings.NewService(NewRouterSettings(db)).Save(t.Context(), pending); err != nil {
		t.Fatal(err)
	}

	s := NewPublications(db)
	plan := groupPlanFromRead(t, db, "A")
	plan.Edit = &configuration.TemplateDraft{ID: "A", Sections: []configuration.TemplateSection{{Name: "stats"}}}
	plan.Members[0].NewRevision = groupFiles(" {\"log\": {}}\r\n")
	plan.Members[1].OrderBasis.TemplateVersion = 2
	result, err := s.CommitTemplateGroup(t.Context(), plan)
	want := &publication.TemplateGroupResult{TemplateID: "A", Version: 2, Routers: []publication.GroupRouterResult{
		{RouterID: "router", Revision: 2, Changed: true}, {RouterID: "second", Revision: 1},
	}}
	if err != nil || !reflect.DeepEqual(result, want) {
		t.Fatalf("commit = %#v, %v; want %#v", result, err, want)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	group, err := NewPublications(reopened).ReadTemplateGroup(t.Context(), "A")
	if err != nil || len(group.Members) != 2 || group.Accepted.Version != 2 || group.TemplateDraft.Version != 2 ||
		!reflect.DeepEqual(group.Accepted.TemplateDraft, group.TemplateDraft.TemplateDraft) ||
		len(group.Accepted.Sections) != 1 || group.Accepted.Sections[0].Name != "stats" {
		t.Fatalf("reopened group = %#v, %v", group, err)
	}
	first, second := group.Members[0], group.Members[1]
	if first.Desired.Number != 2 || !reflect.DeepEqual(first.Desired.Catalog, *plan.Members[0].NewRevision) ||
		second.Desired.Number != 1 || second.Active.Draft.Name != "keep me" ||
		second.Active.Draft.PersonalOrder == nil || second.Active.OrderBasis.TemplateVersion != 2 {
		t.Fatalf("wrong stored group outcomes: %#v", group.Members)
	}
	current, found, err := routersettings.NewService(NewRouterSettings(reopened)).Get(t.Context(), pending.ID)
	if err != nil || !found || !reflect.DeepEqual(current, pending) {
		t.Fatalf("pending draft = %#v, %t, %v", current, found, err)
	}
}

func TestPublicationGroupRollsBackASecondRouterWriteFailure(t *testing.T) {
	db, path := openPublicationDB(t)
	storeGroupPublication(t, db)
	store := NewPublications(db)
	before, err := store.ReadTemplateGroup(t.Context(), "A")
	if err != nil {
		t.Fatal(err)
	}
	plan := groupPlanFromRead(t, db, "A")
	plan.Edit = &configuration.TemplateDraft{ID: "A", Sections: []configuration.TemplateSection{{Name: "stats"}}}
	plan.Members[0].NewRevision = groupFiles("{\"log\":{}}\n")
	plan.Members[1].NewRevision = groupFiles("{\"log\":{}}\n")
	execSQL(t, db, `CREATE TRIGGER reject_second_revision BEFORE INSERT ON desired_router_revisions
		WHEN NEW.router_id = 'second' AND EXISTS (SELECT 1 FROM desired_router_revisions WHERE router_id='router' AND revision=2)
		BEGIN SELECT RAISE(ABORT, 'demo-private-marker'); END`)

	result, err := store.CommitTemplateGroup(t.Context(), plan)
	if result != nil || err == nil || err.Error() != operationError("write publication", nil).Error() || strings.Contains(err.Error(), "demo-private-marker") {
		t.Fatalf("failure = %#v, %v", result, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	after, err := NewPublications(other).ReadTemplateGroup(t.Context(), "A")
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("partial group persisted: %#v, %v", after, err)
	}
	if count := readText(t, other, "SELECT count(*) FROM desired_router_revisions"); count != "2" {
		t.Fatalf("first router revision was not rolled back: %s", count)
	}
}

func TestPublicationGroupAcceptsAStoredTemplateWithNoMembers(t *testing.T) {
	db, _ := openPublicationDB(t)
	saveTemplateDraft(t, db, "A")
	s := NewPublications(db)
	for range 2 {
		plan := groupPlanFromRead(t, db, "A")
		got, err := s.CommitTemplateGroup(t.Context(), plan)
		if err != nil || got == nil || got.Version != 1 || got.Routers == nil || len(got.Routers) != 0 {
			t.Fatalf("empty group = %#v, %v", got, err)
		}
	}
	if count := readText(t, db, "SELECT count(*) FROM accepted_templates"); count != "1" {
		t.Fatalf("same accepted version was rewritten: %s", count)
	}
	if count := readText(t, db, "SELECT count(*) FROM desired_router_revisions"); count != "0" {
		t.Fatalf("empty group produced revisions: %s", count)
	}
}

func TestPublicationGroupRejectsDivergentSourceWithSameVersion(t *testing.T) {
	db, _ := openPublicationDB(t)
	storeCurrentPublication(t, db)
	execSQL(t, db, `UPDATE templates SET draft = x'5b7b226e616d65223a227374617473222c22656c656d656e7473223a5b5d7d5d' WHERE id='A'`)
	got, err := NewPublications(db).ReadTemplateGroup(t.Context(), "A")
	if got != nil || !errors.Is(err, publication.ErrStoredState) {
		t.Fatalf("same-version source divergence = %#v, %v", got, err)
	}
}

func TestPublicationGroupCannotResetPersonalOrder(t *testing.T) {
	db, _ := openPublicationDB(t)
	storeCurrentPublication(t, db)
	active := routersettings.Draft{ID: "router", TemplateID: "A", Name: "old", PersonalOrder: []string{}}
	basis := publication.OrderBasis{TemplateID: "A", TemplateVersion: 1, SharedRuleIDs: []string{}}
	raw, _ := json.Marshal(encodeRouterDraft(active))
	rawBasis, _ := json.Marshal(basis)
	if _, err := db.Exec("UPDATE active_router_publications SET settings=?,order_basis=?", raw, rawBasis); err != nil {
		t.Fatal(err)
	}
	plan := groupPlanFromRead(t, db, "A")
	plan.Members[0].PersonalOrder, plan.Members[0].OrderBasis = nil, nil
	got, err := NewPublications(db).CommitTemplateGroup(t.Context(), plan)
	if got != nil || !errors.Is(err, publication.ErrInvalidState) {
		t.Fatalf("ordinary shared publication reset personal order: %#v, %v", got, err)
	}
}
