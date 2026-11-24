package server_test

import (
	"bytes"
	"crypto/sha256"
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
