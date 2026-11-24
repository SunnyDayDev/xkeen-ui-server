package sqlite

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

func TestPublicationMigrationPreservesDrafts(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "earlier.sqlite")
	prior, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer prior.Close()
	if err := migrate(ctx, prior, migrations[:4]); err != nil {
		t.Fatal(err)
	}
	execSQL(t, prior, "INSERT INTO templates (id,draft,version) VALUES ('tpl',x'5b5d',7)")
	draft := routersettings.Draft{ID: "router", TemplateID: "tpl", PersonalOrder: []string{}}
	raw, err := json.Marshal(encodeRouterDraft(draft))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prior.Exec("INSERT INTO router_settings (id,snapshot) VALUES (?,?)", draft.ID, raw); err != nil {
		t.Fatal(err)
	}
	if err := prior.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := readText(t, db, "SELECT version FROM templates WHERE id='tpl'"); got != "7" {
		t.Errorf("draft version = %q", got)
	}
	if got := readText(t, db, "SELECT CAST(snapshot AS TEXT) FROM router_settings WHERE id='router'"); got != string(raw) {
		t.Errorf("migration rewrote router draft: %q", got)
	}
	for _, table := range []string{"accepted_templates", "active_router_publications", "desired_router_revisions"} {
		var n int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s count=%d, error=%v", table, n, err)
		}
	}
	var generation int
	if err := db.QueryRow("SELECT generation FROM router_settings WHERE id='router'").Scan(&generation); err != nil || generation != 1 {
		t.Fatalf("migrated generation=%d, error=%v", generation, err)
	}
	store := NewRouterSettings(db)
	if err := store.Save(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, db, "SELECT generation FROM router_settings WHERE id='router'"); got != "2" {
		t.Fatalf("saved generation = %q", got)
	}
}
