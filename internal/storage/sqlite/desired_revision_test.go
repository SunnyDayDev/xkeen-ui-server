package sqlite

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
)

func TestDesiredRevisionReadsOnlyLatestCommittedFiles(t *testing.T) {
	ctx := t.Context()
	db, path := openPublicationDB(t)
	writer, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	read := NewPublications(db).ReadDesired
	if rev, err := read(ctx, "router.example"); rev != nil || err != nil {
		t.Fatalf("absent revision = %v, %v", rev, err)
	}

	write := func(id string, number int, raw []byte) {
		t.Helper()
		_, err := writer.ExecContext(ctx, `INSERT INTO desired_router_revisions(router_id, revision, profile, files) VALUES (?, ?, '26.3.27', ?)`, id, number, raw)
		if err != nil {
			t.Fatal(err)
		}
	}
	encoded := func(value string) []byte {
		raw, err := json.Marshal([]publicationFileRecord{{Name: "01_log.json", Bytes: []byte(value)}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	write("router.example", 1, encoded("{}"))
	write("other.example", 4, encoded("[]"))
	second := encoded(" { }\n")
	done := make(chan error, 1)
	go func() {
		_, err := writer.ExecContext(ctx, `INSERT INTO desired_router_revisions(router_id, revision, profile, files) VALUES ('router.example', 2, '26.3.27', ?)`, second)
		done <- err
	}()
	for i := 0; i < 10; i++ {
		got, err := read(ctx, "router.example")
		if err != nil || got == nil || got.Number < 1 || got.Number > 2 || len(got.Catalog.Files) != 1 {
			t.Fatalf("concurrent read = %v, %v", got, err)
		}
		want := map[int64]string{1: "{}", 2: " { }\n"}[got.Number]
		if !bytes.Equal(got.Catalog.Files[0].Content, []byte(want)) {
			t.Fatal("files and number are from different snapshots")
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	latest, err := read(ctx, "router.example")
	if err != nil || latest == nil || latest.Number != 2 || latest.Catalog.Profile != "26.3.27" {
		t.Fatalf("latest revision = %v, %v", latest, err)
	}
	write("router.example", 3, []byte(`[{"Name":"../danger.json","Bytes":"e30="}]`))
	if got, err := read(ctx, "router.example"); got != nil || !errors.Is(err, publication.ErrStoredState) {
		t.Fatalf("damaged latest revision = %v, %v", got, err)
	}
}
