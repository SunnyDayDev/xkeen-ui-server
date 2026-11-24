package server_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/server"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func TestExternalHTTPSReadAndRevocation(t *testing.T) {
	ctx := t.Context()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "drafts.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := templates.New(sqlite.NewTemplateRepository(db))
	if _, err = service.Create(ctx, configuration.TemplateDraft{ID: "empty example"}); err != nil {
		t.Fatal(err)
	}
	admin := sqlite.NewWebAdmin(db)
	session := [32]byte{4}
	if err = admin.SaveVerifier(ctx, webadmin.Verifier{Version: 1, Salt: [16]byte{1}, Digest: [32]byte{2}}, false); err != nil {
		t.Fatal(err)
	}
	if err = admin.Issue(ctx, 1, session, [32]byte{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	store := sqlite.NewExternalAccess(db, time.Now)
	serverHTTP := httptest.NewTLSServer(server.NewExternalAccessHandler("", store, server.NewExternalTemplates(store), http.NotFoundHandler()))
	defer serverHTTP.Close()
	read := func(token string, want int) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, "GET", serverHTTP.URL+"/api/v1/templates/item?templateId=empty+example", nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := serverHTTP.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != want {
			t.Fatal("HTTP status", resp.StatusCode, err)
		}
		if want == 200 {
			if !bytes.Equal(raw, []byte(`{"id":"empty example","version":"1","sections":[]}`)) || resp.Header.Get("ETag") != fmt.Sprintf("\"%x\"", sha256.Sum256(raw)) {
				t.Fatal("wrong complete empty snapshot")
			}
		} else if resp.Header.Get("ETag") != "" || bytes.Contains(raw, []byte("empty example")) {
			t.Fatal("draft leaked")
		}
	}
	read("", 404)
	state, _, err := store.Owner(ctx, session, &externalaccess.Change{Version: 1, Enabled: true, Protected: true})
	if err != nil {
		t.Fatal(err)
	}
	read("", 401)
	state, token, err := store.Owner(ctx, session, &externalaccess.Change{Version: state.Version, Rotate: true})
	if err != nil {
		t.Fatal(err)
	}
	read(token, 200)
	permit, err := store.Admit(ctx, []string{"Bearer " + token})
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = store.Owner(ctx, session, &externalaccess.Change{Version: state.Version, Enabled: true, Protected: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadExternalTemplate(ctx, permit, "empty example"); err != externalaccess.ErrUnauthorized {
		t.Fatal("stale permit disclosed snapshot")
	}
	read("", 200)
	_, _, err = store.Owner(ctx, session, &externalaccess.Change{Version: state.Version, Protected: false})
	if err != nil {
		t.Fatal(err)
	}
	read(token, 404)
}

func TestExternalHTTPSConditionalPatch(t *testing.T) {
	ctx := t.Context()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "patch.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const uid = "550e8400-e29b-41d4-a716-446655440000"
	service := templates.New(sqlite.NewTemplateRepository(db))
	_, err = service.Create(ctx, configuration.TemplateDraft{ID: "example", Sections: []configuration.TemplateSection{{Name: "rules", Elements: []configuration.TemplateElement{{Element: configuration.Element{ID: uid, Content: "{}"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	admin := sqlite.NewWebAdmin(db)
	session := [32]byte{4}
	if err = admin.SaveVerifier(ctx, webadmin.Verifier{Version: 1, Salt: [16]byte{1}, Digest: [32]byte{2}}, false); err != nil {
		t.Fatal(err)
	}
	if err = admin.Issue(ctx, 1, session, [32]byte{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	store := sqlite.NewExternalAccess(db, time.Now)
	state, _, err := store.Owner(ctx, session, &externalaccess.Change{Version: 1, Enabled: true, Protected: false})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(server.NewExternalAccessHandler("", store, server.NewExternalTemplates(store), http.NotFoundHandler()))
	defer srv.Close()
	call := func(method, tag, body string) (int, string, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, srv.URL+"/api/v1/templates/item?templateId=example", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if tag != "" {
			req.Header.Set("If-Match", tag)
		}
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, res.Header.Get("ETag"), raw
	}
	_, base, _ := call("GET", "", "")
	body := `{"elementId":"` + uid + `","source":"{  \"n\" : 9007199254740993 }"}`
	for _, tc := range []struct {
		tag, body string
		status    int
	}{{"", body, 428}, {"*", body, 400}, {base, `{"elementId":"` + uid + `","source":"bad private example"}`, 422}, {base, `{"elementId":"` + uid + `","source":"{}","source":"null"}`, 400}} {
		status, etag, raw := call("PATCH", tc.tag, tc.body)
		if status != tc.status || etag != "" || bytes.Contains(raw, []byte("private example")) {
			t.Fatal(status, tc.status)
		}
	}
	status, changed, raw := call("PATCH", base, body)
	if status != 200 || changed == base || changed != fmt.Sprintf("\"%x\"", sha256.Sum256(raw)) {
		t.Fatal(status, "response tag")
	}
	if status, _, _ := call("PATCH", base, body); status != 412 {
		t.Fatal(status, "stale CAS")
	}
	_, current, saved := call("GET", "", "")
	if current != changed || !bytes.Equal(saved, raw) {
		t.Fatal("PATCH did not return committed representation")
	}
	permit, err := store.Admit(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := store.ReadExternalTemplate(ctx, permit, "example")
	if err != nil {
		t.Fatal(err)
	}
	// The browser and external client race from the same committed snapshot.
	webCandidate, err := templates.EditElementContent(snap, uid, "[]")
	if err != nil {
		t.Fatal(err)
	}
	webResult := make(chan error, 1)
	go func() {
		_, err := sqlite.NewWebTemplates(db).ReplaceTemplate(ctx, session, time.Now, webCandidate, snap.Version)
		webResult <- err
	}()
	status, _, _ = call("PATCH", current, body)
	webErr := <-webResult
	if !(status == 200 && errors.Is(webErr, templates.ErrVersionConflict) || status == 412 && webErr == nil) {
		t.Fatalf("competing writers %d / %v", status, webErr)
	}
	if _, _, err = store.Owner(ctx, session, &externalaccess.Change{Version: state.Version, Enabled: true, Protected: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceExternalTemplate(ctx, permit, snap.TemplateDraft, snap.Version); err != externalaccess.ErrUnauthorized {
		t.Fatal("stale writer permit", err)
	}
}
