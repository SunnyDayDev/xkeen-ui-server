package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func openPublicationDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "publication.sqlite")
	db, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, path
}

func saveTemplateDraft(t *testing.T, db *sql.DB, id string) *templates.Snapshot {
	t.Helper()
	draft, err := templates.New(NewTemplateRepository(db)).Create(t.Context(), configuration.TemplateDraft{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

func storeRawAccepted(t *testing.T, db *sql.DB, template *templates.Snapshot) {
	t.Helper()
	raw, err := encodeTemplate(template.TemplateDraft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO accepted_templates VALUES (?,?,?)", template.ID, template.Version, raw); err != nil {
		t.Fatal(err)
	}
}

func TestPublicationReadSeparatesDraftAndAcceptedState(t *testing.T) {
	db, path := openPublicationDB(t)
	tpl := saveTemplateDraft(t, db, "A")
	storeRawAccepted(t, db, tpl)
	draft := tpl.TemplateDraft
	draft.Sections = []configuration.TemplateSection{{Name: "stats"}}
	if _, err := templates.New(NewTemplateRepository(db)).Replace(t.Context(), draft, tpl.Version); err != nil {
		t.Fatal(err)
	}

	active := routersettings.Draft{ID: "router", TemplateID: "A", Name: "active", PersonalOrder: []string{}}
	basis := &publication.OrderBasis{TemplateID: "A", TemplateVersion: 1, SharedRuleIDs: []string{}}
	activeRaw, _ := json.Marshal(encodeRouterDraft(active))
	basisRaw, _ := json.Marshal(basis)
	if _, err := db.Exec("INSERT INTO active_router_publications VALUES (?,?,?,?,?)", active.ID, active.TemplateID, 2, activeRaw, basisRaw); err != nil {
		t.Fatal(err)
	}
	pending := routersettings.Draft{ID: active.ID, TemplateID: "B", Name: "pending"}
	if err := routersettings.NewService(NewRouterSettings(db)).Save(t.Context(), pending); err != nil {
		t.Fatal(err)
	}
	files := []configuration.CatalogFile{{Name: "01_log.json", Content: []byte(" { \"log\": {} }\r\n")}}
	revisionRaw, _ := json.Marshal([]publicationFileRecord{{Name: files[0].Name, Bytes: files[0].Content}})
	if _, err := db.Exec("INSERT INTO desired_router_revisions VALUES (?,?,?,?)", active.ID, 1, configuration.XrayIdentityVersion, revisionRaw); err != nil {
		t.Fatal(err)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewPublications(db)
	request := publication.ReadRequest{RouterID: active.ID, IncludeTemplateDraft: true, IncludeRouterDraft: true}
	got, err := store.Read(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got.Accepted.Version != 1 || got.TemplateDraft.Version != 2 || len(got.Accepted.Sections) != 0 ||
		!reflect.DeepEqual(got.Active.Draft, active) || !reflect.DeepEqual(got.Active.OrderBasis, basis) ||
		got.Active.Generation != 2 || !reflect.DeepEqual(got.RouterDraft.Draft, pending) ||
		!got.TemplateDraftRequested || !got.RouterDraftRequested || got.RouterDraft.Generation != 1 ||
		got.Desired.Number != 1 || !reflect.DeepEqual(got.Desired.Catalog.Files, files) {
		t.Fatalf("read returned different states: %#v", got)
	}
	got.Desired.Catalog.Files[0].Content[0] = 'X'
	got.Active.OrderBasis.SharedRuleIDs = append(got.Active.OrderBasis.SharedRuleIDs, "other")
	again, err := store.Read(t.Context(), publication.ReadRequest{RouterID: active.ID})
	if err != nil || len(again.Active.OrderBasis.SharedRuleIDs) != 0 || !reflect.DeepEqual(again.Desired.Catalog.Files, files) ||
		again.TemplateDraftRequested || again.RouterDraftRequested || again.RouterDraft != nil {
		t.Fatalf("read alias or optional inputs included: %#v, %v", again, err)
	}
	if _, err := db.Exec("UPDATE desired_router_revisions SET files = x'6e756c6c'"); err != nil {
		t.Fatal(err)
	}
	if result, err := store.Read(t.Context(), request); result != nil || !errors.Is(err, publication.ErrStoredState) {
		t.Fatalf("damaged state returned %#v, %v", result, err)
	}
}

func TestPublicationReadDoesNotActivateAnExistingDraft(t *testing.T) {
	db, _ := openPublicationDB(t)
	saveTemplateDraft(t, db, "A")
	pending := routersettings.Draft{ID: "router", TemplateID: "A"}
	if err := routersettings.NewService(NewRouterSettings(db)).Save(t.Context(), pending); err != nil {
		t.Fatal(err)
	}
	got, err := NewPublications(db).Read(t.Context(), publication.ReadRequest{
		RouterID: pending.ID, InitialTemplateID: "A", IncludeRouterDraft: true, IncludeTemplateDraft: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Active != nil || got.Accepted != nil || got.Desired != nil ||
		got.TemplateDraft == nil || got.RouterDraft == nil {
		t.Fatalf("unexpected initial state: %#v", got)
	}
}

func storeCurrentPublication(t *testing.T, db *sql.DB) publication.CommitPlan {
	t.Helper()
	template := saveTemplateDraft(t, db, "A")
	storeRawAccepted(t, db, template)
	active := routersettings.Draft{ID: "router", TemplateID: "A", Name: "old"}
	activeRaw, _ := json.Marshal(encodeRouterDraft(active))
	if _, err := db.Exec("INSERT INTO active_router_publications VALUES (?,?,?,?,NULL)", active.ID, active.TemplateID, 1, activeRaw); err != nil {
		t.Fatal(err)
	}
	files, _ := json.Marshal([]publicationFileRecord{{Name: "01_log.json", Bytes: []byte("{}\n")}})
	if _, err := db.Exec("INSERT INTO desired_router_revisions VALUES (?,?,?,?)", active.ID, 1, configuration.XrayIdentityVersion, files); err != nil {
		t.Fatal(err)
	}
	active.Name = "new"
	return publication.CommitPlan{
		Expected: publication.Expectations{RouterID: active.ID, TemplateID: active.TemplateID, AcceptedVersion: 1, ActiveGeneration: 1, DesiredNumber: 1},
		Settings: active,
		NewRevision: &publication.VerifiedFiles{Profile: configuration.XrayIdentityVersion,
			Files: []configuration.CatalogFile{{Name: "01_log.json", Content: []byte(" { \"log\": {} }\r\n")}}},
	}
}

func TestPublicationCommitPreservesRevisionBytesAndNoOp(t *testing.T) {
	db, path := openPublicationDB(t)
	plan := storeCurrentPublication(t, db)
	store := NewPublications(db)
	number, err := store.Commit(t.Context(), plan)
	if err != nil || number != 2 {
		t.Fatalf("new revision: %d, %v", number, err)
	}
	plan.Expected.DesiredNumber, plan.Expected.ActiveGeneration = 2, 2
	plan.Settings.Name = "accepted edit without new files"
	plan.NewRevision = nil
	if number, err := store.Commit(t.Context(), plan); err != nil || number != 2 {
		t.Fatalf("keep desired revision: %d, %v", number, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := NewPublications(db).Read(t.Context(), publication.ReadRequest{RouterID: plan.Settings.ID})
	if err != nil || got.Desired.Number != 2 || got.Active.Generation != 3 ||
		got.Active.Draft.Name != "accepted edit without new files" ||
		string(got.Desired.Catalog.Files[0].Content) != " { \"log\": {} }\r\n" {
		t.Fatalf("reopened publication: %#v, %v", got, err)
	}
	var raw []byte
	if err := db.QueryRow("SELECT files FROM desired_router_revisions WHERE revision=1").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var earliest []publicationFileRecord
	if err := json.Unmarshal(raw, &earliest); err != nil || string(earliest[0].Bytes) != "{}\n" {
		t.Fatalf("first revision changed: %q, %v", raw, err)
	}
}

func TestPublicationOrdinaryCommitCannotChangeAssignment(t *testing.T) {
	db, _ := openPublicationDB(t)
	plan := storeCurrentPublication(t, db)
	other := saveTemplateDraft(t, db, "B")
	storeRawAccepted(t, db, other)
	store := NewPublications(db)
	plan.Settings.TemplateID = "B"
	if _, err := store.Commit(t.Context(), plan); !errors.Is(err, publication.ErrInvalidState) {
		t.Fatalf("new settings do not match expected template: %v", err)
	}
	plan.Expected.TemplateID = "B"
	if _, err := store.Commit(t.Context(), plan); !errors.Is(err, publication.ErrConflict) {
		t.Fatalf("ordinary publication could change active assignment: %v", err)
	}
	plan.Expected.TemplateID, plan.Settings.TemplateID = "A", "A"
	plan.Settings.ID = "other-router"
	if _, err := store.Commit(t.Context(), plan); !errors.Is(err, publication.ErrInvalidState) {
		t.Fatalf("expected state from another router reused: %v", err)
	}
	if got := readText(t, db, "SELECT template_id FROM active_router_publications"); got != "A" {
		t.Fatalf("active template changed: %q", got)
	}
}

func TestPublicationCompetingCommitsAndPendingDraft(t *testing.T) {
	db, path := openPublicationDB(t)
	plan := storeCurrentPublication(t, db)
	other, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	pending := routersettings.Draft{ID: plan.Settings.ID, TemplateID: "B", Name: "pending before"}
	service := routersettings.NewService(NewRouterSettings(other))
	if err := service.Save(t.Context(), pending); err != nil {
		t.Fatal(err)
	}
	input, err := NewPublications(db).Read(t.Context(), publication.ReadRequest{RouterID: pending.ID, IncludeRouterDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	stale := plan
	stale.Expected.RouterDraftGeneration = &input.RouterDraft.Generation
	pending.Name = "pending after"
	if err := service.Save(t.Context(), pending); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPublications(db).Commit(t.Context(), stale); !errors.Is(err, publication.ErrConflict) {
		t.Fatalf("used stale draft was accepted: %v", err)
	}

	start := make(chan struct{})
	type outcome struct {
		name string
		err  error
	}
	done := make(chan outcome, 2)
	for i, connection := range []*sql.DB{db, other} {
		candidate := plan
		candidate.Settings.Name = []string{"winner A", "winner B"}[i]
		go func() {
			<-start
			_, err := NewPublications(connection).Commit(t.Context(), candidate)
			done <- outcome{candidate.Settings.Name, err}
		}()
	}
	close(start)
	var winner string
	var conflicts int
	for range 2 {
		res := <-done
		if res.err == nil {
			winner = res.name
		} else if errors.Is(res.err, publication.ErrConflict) {
			conflicts++
		} else {
			t.Error(res.err)
		}
	}
	got, err := NewPublications(other).Read(t.Context(), publication.ReadRequest{RouterID: pending.ID, IncludeRouterDraft: true})
	if conflicts != 1 || winner == "" || err != nil || got.Active.Draft.Name != winner ||
		got.Desired.Number != 2 || !reflect.DeepEqual(got.RouterDraft.Draft, pending) {
		t.Fatalf("conflicts=%d winner=%q persisted=%#v err=%v", conflicts, winner, got, err)
	}
}

func TestPublicationRollsBackAfterActiveWrite(t *testing.T) {
	db, _ := openPublicationDB(t)
	plan := storeCurrentPublication(t, db)
	execSQL(t, db, `CREATE TRIGGER reject_test_revision BEFORE INSERT ON desired_router_revisions
		BEGIN SELECT RAISE(ABORT, 'demo-private-value'); END;`)
	store := NewPublications(db)
	if _, err := store.Commit(t.Context(), plan); err == nil || strings.Contains(err.Error(), "demo-private-value") {
		t.Fatalf("unsafe or missing write error: %v", err)
	}
	got, err := store.Read(t.Context(), publication.ReadRequest{RouterID: plan.Settings.ID})
	if err != nil || got.Desired.Number != 1 || got.Active.Generation != 1 || got.Active.Draft.Name != "old" {
		t.Fatalf("partial publication: %#v, %v", got, err)
	}
}

func firstPublication(routerID string, template *templates.Snapshot, bootstrap bool) publication.CommitPlan {
	plan := publication.CommitPlan{
		FirstConfirmation: true, AcceptInitialTemplate: bootstrap,
		Expected: publication.Expectations{RouterID: routerID, TemplateID: template.ID, AcceptedVersion: template.Version},
		Settings: routersettings.Draft{ID: routerID, TemplateID: template.ID},
		NewRevision: &publication.VerifiedFiles{Profile: configuration.XrayIdentityVersion,
			Files: []configuration.CatalogFile{{Name: "01_log.json", Content: []byte("{}\n")}}},
	}
	if bootstrap {
		plan.Expected.AcceptedVersion = 0
		plan.Expected.TemplateDraftVersion = &template.Version
	}
	return plan
}

func TestPublicationFirstConfirmationUsesAcceptedTemplate(t *testing.T) {
	db, _ := openPublicationDB(t)
	template := saveTemplateDraft(t, db, "A")
	storeRawAccepted(t, db, template)
	s := NewPublications(db)
	plan := firstPublication("first", template, false)
	withoutRevision := plan
	withoutRevision.NewRevision = nil
	if _, err := s.Commit(t.Context(), withoutRevision); !errors.Is(err, publication.ErrInvalidState) {
		t.Errorf("first confirmation without new revision: %v", err)
	}
	if number, err := s.Commit(t.Context(), plan); err != nil || number != 1 {
		t.Fatalf("first confirmation number=%d err=%v", number, err)
	}
	if _, err := s.Commit(t.Context(), plan); !errors.Is(err, publication.ErrConflict) {
		t.Fatalf("already assigned router passed first confirmation: %v", err)
	}
	result, err := s.Read(t.Context(), publication.ReadRequest{RouterID: plan.Settings.ID})
	if err != nil || result.Active.Draft.TemplateID != template.ID ||
		result.Accepted.Version != template.Version || result.Desired.Number != 1 {
		t.Fatalf("published state %#v, %v", result, err)
	}
}

func TestPublicationBootstrapIsAtomicAndDoesNotActivateOtherDrafts(t *testing.T) {
	db, path := openPublicationDB(t)
	template := saveTemplateDraft(t, db, "A")
	other, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	plans := []publication.CommitPlan{firstPublication("first", template, true), firstPublication("second", template, true)}
	for _, plan := range plans {
		if err := routersettings.NewService(NewRouterSettings(db)).Save(t.Context(), plan.Settings); err != nil {
			t.Fatal(err)
		}
	}

	execSQL(t, db, `CREATE TRIGGER ignore_test_revision BEFORE INSERT ON desired_router_revisions
		BEGIN SELECT RAISE(IGNORE); END;`)
	if _, err := NewPublications(db).Commit(t.Context(), plans[0]); err == nil {
		t.Fatal("ignored desired insert should roll back the bootstrap")
	}
	state, err := NewPublications(db).Read(t.Context(), publication.ReadRequest{
		RouterID: plans[0].Settings.ID, InitialTemplateID: template.ID, IncludeRouterDraft: true,
	})
	if err != nil || state.RouterDraft == nil || state.Accepted != nil || state.Desired != nil || state.Active != nil {
		t.Fatalf("failed bootstrap changed stored state: %#v, %v", state, err)
	}
	execSQL(t, db, "DROP TRIGGER ignore_test_revision")

	start := make(chan struct{})
	type result struct {
		id  string
		err error
	}
	done := make(chan result, 2)
	for i, connection := range []*sql.DB{db, other} {
		plan := plans[i]
		go func() {
			<-start
			_, err := NewPublications(connection).Commit(t.Context(), plan)
			done <- result{plan.Settings.ID, err}
		}()
	}
	close(start)
	var winner, rejected string
	for range 2 {
		result := <-done
		if result.err == nil {
			winner = result.id
		} else if errors.Is(result.err, publication.ErrConflict) {
			rejected = result.id
		} else {
			t.Error(result.err)
		}
	}
	if winner == "" || rejected == "" {
		t.Fatalf("winner=%q rejected=%q", winner, rejected)
	}
	state, err = NewPublications(other).Read(t.Context(), publication.ReadRequest{
		RouterID: rejected, InitialTemplateID: template.ID, IncludeRouterDraft: true,
	})
	if err != nil || state.Accepted.Version != template.Version || state.RouterDraft.Draft.ID != rejected ||
		state.Active != nil || state.Desired != nil {
		t.Fatalf("other draft was activated: %#v, %v", state, err)
	}
}

func TestPublicationRejectsUnsafeStoredFileName(t *testing.T) {
	db, _ := openPublicationDB(t)
	plan := storeCurrentPublication(t, db)
	plan.NewRevision.Files[0].Name = "../01_log.json"
	store := NewPublications(db)
	if _, err := store.Commit(t.Context(), plan); !errors.Is(err, publication.ErrInvalidState) {
		t.Errorf("unsafe filename was accepted for write: %v", err)
	}
	for _, name := range []string{"../01_log.json", "/01_log.json", "sub/01_log.json", "sub\\01_log.json", "file\x00.json"} {
		raw, _ := json.Marshal([]publicationFileRecord{{Name: name, Bytes: []byte("{}")}})
		if _, err := db.Exec("UPDATE desired_router_revisions SET files = ?", raw); err != nil {
			t.Fatal(err)
		}
		if result, err := store.Read(t.Context(), publication.ReadRequest{RouterID: plan.Settings.ID}); result != nil ||
			!errors.Is(err, publication.ErrStoredState) {
			t.Errorf("unsafe filename read as state: %q, %#v, %v", name, result, err)
		}
	}
}

func TestPublicationRejectsStoredBasisForDifferentOrderAtCurrentVersion(t *testing.T) {
	db, _ := openPublicationDB(t)
	plan := storeCurrentPublication(t, db)
	unrelatedID := "550e8400-e29b-41d4-a716-446655440000"
	plan.Settings.PersonalOrder = []string{unrelatedID}
	basis := publication.OrderBasis{TemplateID: "A", TemplateVersion: 1, SharedRuleIDs: []string{unrelatedID}}
	active, _ := json.Marshal(encodeRouterDraft(plan.Settings))
	rawBasis, _ := json.Marshal(basis)
	if _, err := db.Exec("UPDATE active_router_publications SET settings = ?, order_basis = ?", active, rawBasis); err != nil {
		t.Fatal(err)
	}
	got, err := NewPublications(db).Read(t.Context(), publication.ReadRequest{RouterID: plan.Settings.ID})
	if got != nil || !errors.Is(err, publication.ErrStoredState) {
		t.Fatalf("wrong basis accepted: %#v, %v", got, err)
	}
}
