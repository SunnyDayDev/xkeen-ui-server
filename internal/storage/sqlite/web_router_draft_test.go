package sqlite

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

func TestWebRouterDraftWriterRechecksVisibilityAndEveryGeneration(t *testing.T) {
	ctx := t.Context()
	db, _, digest, issued := fixtureWebTemplateWriter(t)
	plan := storeCurrentPublication(t, db)
	id := plan.Settings.ID
	if _, err := db.ExecContext(ctx, `INSERT INTO agent_contacts (router_id,contact_ns,report,applied,uncertain) VALUES (?, ?,NULL,0,0)`, id, issued.UnixNano()); err != nil {
		t.Fatal(err)
	}
	writer := NewWebRouterDrafts(db)
	clock := fixedWebTime(issued.Add(time.Minute))
	base, err := writer.Read(ctx, digest, clock, id)
	if err != nil {
		t.Fatal(err)
	}
	if base.Basis.RouterID != id || base.Basis.TemplateID != "A" || base.Basis.ActiveGeneration != 1 || base.Basis.AcceptedVersion != 1 || base.Basis.DraftGeneration != 0 {
		t.Fatalf("incorrect basis: %+v", base.Basis)
	}
	candidate := base.Editable
	candidate.Name = "changed draft"
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER suppress_router_draft BEFORE INSERT ON router_settings
		BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	generation, err := writer.Write(ctx, digest, clock, base.Basis, candidate)
	if err == nil || generation != 0 {
		t.Fatalf("suppressed insert reported a successful draft: generation=%d error=%v", generation, err)
	}
	if _, err := db.ExecContext(ctx, "DROP TRIGGER suppress_router_draft"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		generation int64
		err        error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			generation, err := writer.Write(ctx, digest, clock, base.Basis, candidate)
			results <- result{generation, err}
		}()
	}
	a, b := <-results, <-results
	if a.generation+b.generation != 1 ||
		!(a.err == nil && errors.Is(b.err, webadmin.ErrRouterDraftConflict) || b.err == nil && errors.Is(a.err, webadmin.ErrRouterDraftConflict)) {
		t.Fatalf("competing first writes: %v / %v", a, b)
	}
	reloaded, err := writer.Read(ctx, digest, clock, id)
	if err != nil || reloaded.Editable.Name != candidate.Name || reloaded.Basis.DraftGeneration != 1 {
		t.Fatalf("missing committed draft: %v", err)
	}
	for _, wrong := range []webadmin.RouterDraftBasis{
		{RouterID: id, TemplateID: "B", ActiveGeneration: 1, AcceptedVersion: 1, DraftGeneration: 1},
		{RouterID: id, TemplateID: "A", ActiveGeneration: 2, AcceptedVersion: 1, DraftGeneration: 1},
		{RouterID: id, TemplateID: "A", ActiveGeneration: 1, AcceptedVersion: 2, DraftGeneration: 1},
	} {
		if _, err := writer.Write(ctx, digest, clock, wrong, candidate); !errors.Is(err, webadmin.ErrRouterDraftConflict) {
			t.Fatalf("wrong scope/version accepted: %+v %v", wrong, err)
		}
	}
	if err := NewRouterVisibility(db).Hide(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(ctx, digest, clock, reloaded.Basis, candidate); !errors.Is(err, webadmin.ErrRouterDraftMissing) {
		t.Fatalf("hidden router mutation: %v", err)
	}
	if err := NewWebAdmin(db).SaveVerifier(ctx, webadmin.Verifier{Version: 1}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(ctx, digest, clock, reloaded.Basis, candidate); !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("revoked session mutation: %v", err)
	}
	saved, ok, err := NewRouterSettings(db).Get(ctx, id)
	if err != nil || !ok || saved.Name != "changed draft" {
		t.Fatalf("rejected save changed the existing draft: %v", err)
	}
}

func TestWebRouterRulesKeepTheEditableOrderBasisAcrossReopen(t *testing.T) {
	ctx := t.Context()
	db, _, digest, issued := fixtureWebTemplateWriter(t)
	plan := storeCurrentPublication(t, db)
	id := plan.Settings.ID
	if _, err := db.ExecContext(ctx, `INSERT INTO agent_contacts (router_id,contact_ns,report,applied,uncertain) VALUES (?, ?,NULL,0,0)`, id, issued.UnixNano()); err != nil {
		t.Fatal(err)
	}
	writer := NewWebRouterDrafts(db)
	clock := fixedWebTime(issued.Add(time.Minute))
	base, err := writer.Read(ctx, digest, clock, id)
	if err != nil {
		t.Fatal(err)
	}
	const local = "d21e8a4a-9eb3-4f69-8c31-5dd4d77fdce2"
	raw := json.RawMessage(" {\r\n \"weight\":9007199254740993\n}\r\n")
	candidate := base.Editable
	candidate.PersonalOrder = []string{local}
	candidate.Groups = []routersettings.Group{{Name: "rules", Local: []routersettings.LocalElement{{ID: local, Content: raw}}}}
	basis := &publication.OrderBasis{TemplateID: "A", TemplateVersion: 1, SharedRuleIDs: []string{}}
	generation, err := writer.WriteOrder(ctx, digest, clock, base.Basis, candidate, basis)
	if err != nil || generation != 1 {
		t.Fatalf("first local write: %d / %v", generation, err)
	}
	if _, err := writer.WriteOrder(ctx, digest, clock, base.Basis, candidate, basis); !errors.Is(err, webadmin.ErrRouterDraftConflict) {
		t.Fatalf("stale second local write: %v", err)
	}
	var path string
	if err := db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	writer = NewWebRouterDrafts(reopened)
	loaded, err := writer.Read(ctx, digest, clock, id)
	if err != nil || !reflect.DeepEqual(loaded.OrderBasis, basis) || loaded.Editable.PersonalOrder[0] != local ||
		string(loaded.Editable.Groups[0].Local[0].Content) != string(raw) {
		t.Fatalf("reopened exact draft/basis: %+v / %v", loaded, err)
	}
	loaded.Editable.Name = "an ordinary edit"
	if _, err := writer.Write(ctx, digest, clock, loaded.Basis, loaded.Editable); err != nil {
		t.Fatal(err)
	}
	inputs, err := NewPublications(reopened).Read(ctx, publication.ReadRequest{RouterID: id, IncludeRouterDraft: true})
	if err != nil || inputs.RouterDraft == nil || !reflect.DeepEqual(inputs.RouterDraft.OrderBasis, basis) ||
		inputs.RouterDraft.Generation != 2 || inputs.Desired.Number != 1 || inputs.Active.Draft.PersonalOrder != nil {
		t.Fatalf("ordinary edit dropped basis or published: %+v / %v", inputs, err)
	}
	versionTwo := *inputs.Accepted
	versionTwo.Version++
	storeRawAccepted(t, reopened, &versionTwo)
	stale := loaded.Basis
	stale.DraftGeneration = 2
	if _, err := writer.WriteOrder(ctx, digest, clock, stale, candidate, basis); !errors.Is(err, webadmin.ErrRouterDraftConflict) {
		t.Fatalf("changed accepted basis was allowed: %v", err)
	}
	stored, err := writer.Read(ctx, digest, clock, id)
	if err != nil || stored.Basis.AcceptedVersion != 2 || stored.Basis.DraftGeneration != 2 || !reflect.DeepEqual(stored.OrderBasis, basis) {
		t.Fatalf("stale refusal changed saved ordering: %+v %v", stored, err)
	}
}

func TestRouterEditableBasisMigrationPreservesLegacyDraft(t *testing.T) {
	ctx := t.Context()
	path := t.TempDir() + "/prior.sqlite"
	prior, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer prior.Close()
	if err := migrate(ctx, prior, migrations[:len(migrations)-1]); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(encodeRouterDraft(routersettings.Draft{ID: "router", TemplateID: "A", PersonalOrder: []string{}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prior.ExecContext(ctx, "INSERT INTO router_settings (id,snapshot,generation) VALUES ('router',?,7)", raw); err != nil {
		t.Fatal(err)
	}
	if err := prior.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var saved, basis []byte
	var generation int64
	if err := reopened.QueryRowContext(ctx, "SELECT snapshot, generation, order_basis FROM router_settings WHERE id='router'").Scan(&saved, &generation, &basis); err != nil ||
		!bytes.Equal(saved, raw) || basis != nil || generation != 7 {
		t.Fatalf("migration rewrote the legacy record: %v", err)
	}
}

func TestWebRouterOrderResetClearsThePersistedBasis(t *testing.T) {
	ctx := t.Context()
	db, _, digest, issued := fixtureWebTemplateWriter(t)
	plan := storeCurrentPublication(t, db)
	id := plan.Settings.ID
	if _, err := db.ExecContext(ctx, `INSERT INTO agent_contacts (router_id,contact_ns,report,applied,uncertain) VALUES (?, ?,NULL,0,0)`, id, issued.UnixNano()); err != nil {
		t.Fatal(err)
	}
	writer := NewWebRouterDrafts(db)
	clock := fixedWebTime(issued.Add(time.Minute))
	base, err := writer.Read(ctx, digest, clock, id)
	if err != nil {
		t.Fatal(err)
	}
	basis := &publication.OrderBasis{TemplateID: "A", TemplateVersion: 1, SharedRuleIDs: []string{}}
	draft := base.Editable
	draft.PersonalOrder = []string{}
	generation, err := writer.WriteOrder(ctx, digest, clock, base.Basis, draft, basis)
	if err != nil || generation != 1 {
		t.Fatalf("explicit draft: %d / %v", generation, err)
	}
	base.Basis.DraftGeneration = generation
	draft.PersonalOrder = nil
	if _, err := writer.WriteOrder(ctx, digest, clock, base.Basis, draft, basis); err != nil {
		t.Fatalf("cannot restore inheritance: %v", err)
	}
	saved, err := writer.Read(ctx, digest, clock, id)
	if err != nil || saved.Editable.PersonalOrder != nil || saved.OrderBasis != nil || saved.Basis.DraftGeneration != 2 {
		t.Fatalf("reset kept explicit order or its proof: %+v / %v", saved, err)
	}
}
