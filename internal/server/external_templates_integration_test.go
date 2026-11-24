package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
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

func TestExternalTemplateListConsumer(t *testing.T) {
	ctx := t.Context()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "collection.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	admin := sqlite.NewWebAdmin(db)
	session := [32]byte{8}
	if err := admin.SaveVerifier(ctx, webadmin.Verifier{Version: 1, Salt: [16]byte{1}, Digest: [32]byte{2}}, false); err != nil {
		t.Fatal(err)
	}
	if err := admin.Issue(ctx, 1, session, [32]byte{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	store := sqlite.NewExternalAccess(db, time.Now)
	srv := httptest.NewTLSServer(server.NewExternalAccessHandler("", store, server.NewExternalTemplates(store), http.NotFoundHandler()))
	defer srv.Close()
	get := func(path string, want int) string {
		t.Helper()
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != want || !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
			t.Fatal("HTTP result", resp.StatusCode, err)
		}
		return strings.TrimSpace(string(raw))
	}
	const path = "/api/v1/templates"
	get(path, 404)
	state, _, err := store.Owner(ctx, session, &externalaccess.Change{Version: 1, Enabled: true, Protected: false})
	if err != nil {
		t.Fatal(err)
	}
	if got := get(path, 200); got != `{"templates":[]}` {
		t.Fatalf("empty list: %q", got)
	}
	service := templates.New(sqlite.NewTemplateRepository(db))
	for _, id := range []string{"z example", "A+example"} {
		if _, err := service.Create(ctx, configuration.TemplateDraft{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if got := get(path, 200); got != `{"templates":[{"id":"A+example","version":"1"},{"id":"z example","version":"1"}]}` {
		t.Fatalf("non-empty list: %q", got)
	}
	if got := get(path+"/item?templateId=A%2Bexample", 200); !strings.Contains(got, `"id":"A+example"`) {
		t.Fatalf("item: %q", got)
	}
	get(path+"?other=1", 400)
	_, _, err = store.Owner(ctx, session, &externalaccess.Change{Version: state.Version, Enabled: true, Protected: true})
	if err != nil {
		t.Fatal(err)
	}
	get(path, 401)
	state, token, err := store.Owner(ctx, session, &externalaccess.Change{Version: state.Version + 1, Rotate: true})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || !bytes.Contains(raw, []byte(`"id":"A+example"`)) {
		t.Fatal("protected list", resp.StatusCode, err)
	}
	permit, err := store.Admit(ctx, []string{"Bearer " + token})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Owner(ctx, session, &externalaccess.Change{Version: state.Version, Rotate: true}); err != nil {
		t.Fatal(err)
	}
	if rows, err := store.ListExternalTemplates(ctx, permit); rows != nil || !errors.Is(err, externalaccess.ErrUnauthorized) {
		t.Fatal("stale collection permit", err)
	}

}

func TestExternalTemplateCreateConsumer(t *testing.T) {
	ctx := t.Context()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "create.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	admin := sqlite.NewWebAdmin(db)
	session := [32]byte{8}
	if err := admin.SaveVerifier(ctx, webadmin.Verifier{Version: 1, Salt: [16]byte{1}, Digest: [32]byte{2}}, false); err != nil {
		t.Fatal(err)
	}
	if err := admin.Issue(ctx, 1, session, [32]byte{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	store := sqlite.NewExternalAccess(db, time.Now)
	endpoint := server.NewExternalTemplates(store)
	beforeWrite := make(chan func(), 1)
	srv := httptest.NewTLSServer(server.NewExternalAccessHandler("", store, func(w http.ResponseWriter, r *http.Request, p externalaccess.Permit) {
		if r.URL.RawQuery == "stale=1" {
			(<-beforeWrite)()
			r.URL.RawQuery = ""
		}
		endpoint(w, r, p)
	}, http.NotFoundHandler()))
	defer srv.Close()

	const collection = "/api/v1/templates"
	type response struct {
		status                int
		body, location, cache string
		err                   error
	}
	call := func(method, path, body, bearer, contentType string) response {
		req, err := http.NewRequestWithContext(ctx, method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			return response{err: err}
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		res, err := srv.Client().Do(req)
		if err != nil {
			return response{err: err}
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		return response{res.StatusCode, strings.TrimSpace(string(raw)), res.Header.Get("Location"), res.Header.Get("Cache-Control"), err}
	}
	post := func(path, body, bearer string) response {
		return call(http.MethodPost, path, body, bearer, "application/json")
	}
	expect := func(got response, status int, code string) {
		t.Helper()
		if got.err != nil || got.status != status || (code != "" && got.body != fmt.Sprintf(`{"code":%q}`, code)) {
			t.Fatalf("got %d %q %v, want %d %q", got.status, got.body, got.err, status, code)
		}
	}
	change := func(change externalaccess.Change) (externalaccess.Settings, string) {
		t.Helper()
		state, token, err := store.Owner(ctx, session, &change)
		if err != nil {
			t.Fatal(err)
		}
		return state, token
	}
	expect(post(collection, `{"id":"hidden"}`, ""), 404, "not_found")
	state, _ := change(externalaccess.Change{Version: 1, Enabled: true})
	const exactID = " +&/ alpha "
	const body = `{"id":" +&/ alpha "}`
	got := post(collection, body, "")
	expect(got, 201, "")
	if got.location != collection+"/item?"+(url.Values{"templateId": {exactID}}).Encode() || !strings.Contains(got.cache, "no-store") {
		t.Fatalf("create headers %+v", got)
	}
	var created struct {
		ID      string
		Version string
	}
	if err := json.Unmarshal([]byte(got.body), &created); err != nil || created.ID != exactID || created.Version != "1" {
		t.Fatalf("created %+v %v", created, err)
	}
	item := call(http.MethodGet, got.location, "", "", "")
	expect(item, 200, "")
	var read struct {
		ID       string
		Version  string
		Sections []any
	}
	if err := json.Unmarshal([]byte(item.body), &read); err != nil || read.ID != exactID || read.Version != "1" || read.Sections == nil || len(read.Sections) != 0 {
		t.Fatalf("read %+v %v", read, err)
	}
	expect(post(collection, body, ""), 409, "template_exists")
	for _, bad := range []string{`{"id":null}`, `{"ID":"different"}`, `{"id":"one","id":"two"}`, `{"id":"discard","sections":[]}`, `{"id":"discard"} {}`, `{"id":""}`, `{"id":"` + string([]byte{0xff}) + `"}`} {
		expect(post(collection, bad, ""), 400, "invalid_request")
	}
	expect(post(collection+"?", `{"id":"query"}`, ""), 400, "invalid_request")
	expect(call(http.MethodPost, collection, body, "", "text/plain"), 415, "unsupported_media_type")
	expect(post(collection, `{"id":"`+strings.Repeat("x", 70000)+`"}`, ""), 413, "too_large")
	state, _ = change(externalaccess.Change{Version: state.Version, Enabled: true, Protected: true})
	expect(post(collection, `{"id":"protected"}`, ""), 401, "unauthorized")
	state, bearer := change(externalaccess.Change{Version: state.Version, Rotate: true})
	results := make(chan response, 2)
	for range 2 {
		go func() { results <- post(collection, `{"id":"one winner"}`, bearer) }()
	}
	counts := map[int]int{}
	for range 2 {
		res := <-results
		if res.err != nil {
			t.Fatal(res.err)
		}
		counts[res.status]++
	}
	if counts[201] != 1 || counts[409] != 1 {
		t.Fatalf("concurrent responses %v", counts)
	}
	rotate := make(chan error, 1)
	beforeWrite <- func() {
		_, _, err := store.Owner(ctx, session, &externalaccess.Change{Version: state.Version, Rotate: true})
		rotate <- err
	}
	expect(post(collection+"?stale=1", `{"id":"stale"}`, bearer), 401, "unauthorized")
	if err := <-rotate; err != nil {
		t.Fatal(err)
	}
	expect(post(collection, `{"id":"revoked"}`, bearer), 401, "unauthorized")
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM templates").Scan(&count); err != nil || count != 2 {
		t.Fatalf("drafts %d %v", count, err)
	}
	for _, query := range []string{
		"SELECT COUNT(*) FROM active_router_publications",
		"SELECT COUNT(*) FROM desired_router_revisions",
	} {
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("unexpected publication state %d %v", count, err)
		}
	}
}

func TestExternalTemplateAddElementConsumer(t *testing.T) {
	ctx := t.Context()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "append.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	admin := sqlite.NewWebAdmin(db)
	session := [32]byte{8}
	if err = admin.SaveVerifier(ctx, webadmin.Verifier{Version: 1, Salt: [16]byte{1}, Digest: [32]byte{2}}, false); err != nil {
		t.Fatal(err)
	}
	if err = admin.Issue(ctx, 1, session, [32]byte{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	store := sqlite.NewExternalAccess(db, time.Now)
	opened, _, err := store.Owner(ctx, session, &externalaccess.Change{Version: 1, Enabled: true, Protected: true})
	if err != nil {
		t.Fatal(err)
	}
	issued, token, err := store.Owner(ctx, session, &externalaccess.Change{Version: opened.Version, Rotate: true})
	if err != nil {
		t.Fatal(err)
	}
	hook := externalTemplateWriteHook{ExternalAccess: store, beforeReplace: make(chan func(), 1)}
	endpoint := server.NewExternalTemplates(hook)
	srv := httptest.NewTLSServer(server.NewExternalAccessHandler("", store, endpoint, http.NotFoundHandler()))
	defer srv.Close()

	type reply struct {
		status              int
		text, tag, location string
		err                 error
	}
	call := func(method, path, body, tag, bearer string) reply {
		req, err := http.NewRequestWithContext(ctx, method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			return reply{err: err}
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if tag != "" {
			req.Header.Set("If-Match", tag)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		res, err := srv.Client().Do(req)
		if err != nil {
			return reply{err: err}
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		location := res.Header.Get("Content-Location")
		if method == http.MethodPost && path == "/api/v1/templates" {
			location = res.Header.Get("Location")
		}
		return reply{res.StatusCode, strings.TrimSpace(string(data)), res.Header.Get("ETag"), location, err}
	}
	expect := func(got reply, status int, code string) {
		t.Helper()
		if got.err != nil || got.status != status || (code != "" && got.text != fmt.Sprintf(`{"code":%q}`, code)) {
			t.Fatalf("got %d %q %v, want %d %q", got.status, got.text, got.err, status, code)
		}
	}
	collection := call(http.MethodPost, "/api/v1/templates", `{"id":" +&/ alpha "}`, "", token)
	expect(collection, 201, "")
	query := "?" + (url.Values{"templateId": {" +&/ alpha "}}).Encode()
	item := "/api/v1/templates/item" + query
	elements := "/api/v1/templates/item/elements" + query
	empty := call(http.MethodGet, item, "", "", token)
	expect(empty, 200, "")
	firstID := "550e8400-e29b-41d4-a716-446655440000"
	secondID := "550e8400-e29b-41d4-a716-446655440001"
	thirdID := "550e8400-e29b-41d4-a716-446655440002"
	body := func(section, id, source string) string {
		data, err := json.Marshal(map[string]string{"section": section, "elementId": id, "source": source})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	const source = "{ \"large\":9007199254740993, \"tag\": \"${label}\" }\n"
	add := func(body, tag string) reply { return call(http.MethodPost, elements, body, tag, token) }
	expect(add(body("rules", firstID, source), ""), 428, "precondition_required")
	first := add(body("rules", firstID, source), empty.tag)
	expect(first, 201, "")
	if first.location != item || first.tag == "" || first.tag == empty.tag {
		t.Fatalf("first response headers %+v", first)
	}
	fromGET := call(http.MethodGet, item, "", "", token)
	if fromGET.text != first.text || fromGET.tag != first.tag {
		t.Fatalf("first GET receipt %+v %+v", first, fromGET)
	}
	var version2 struct {
		Version  string
		Sections []struct {
			Name     string
			Elements []struct {
				ID, Source string
				Variables  []struct{ Name, Type string }
			}
		}
	}
	if err := json.Unmarshal([]byte(first.text), &version2); err != nil || version2.Version != "2" || len(version2.Sections) != 1 || version2.Sections[0].Name != "rules" || len(version2.Sections[0].Elements) != 1 {
		t.Fatalf("first snapshot %+v %v", version2, err)
	}
	firstElement := version2.Sections[0].Elements[0]
	if firstElement.ID != firstID || firstElement.Source != source || len(firstElement.Variables) != 1 || firstElement.Variables[0].Name != "label" || firstElement.Variables[0].Type != "string" {
		t.Fatalf("first element %+v", firstElement)
	}
	t.Run("definition consumer", func(t *testing.T) {
		expect := func(got reply, status int, code string) {
			t.Helper()
			if got.err != nil || got.status != status || code != "" && got.text != fmt.Sprintf(`{"code":%q}`, code) {
				t.Fatalf("got %d %q %v, want %d %q", got.status, got.text, got.err, status, code)
			}
		}
		const defsID = "definitions.example"
		own := call(http.MethodPost, "/api/v1/templates", `{"id":"definitions.example"}`, "", token)
		expect(own, 201, "")
		resource := own.location
		initial := call(http.MethodGet, resource, "", "", token)
		elem := call(http.MethodPost, "/api/v1/templates/item/elements?templateId="+defsID, body("rules", firstID, source), initial.tag, token)
		expect(elem, 201, "")
		sibling := call(http.MethodPost, "/api/v1/templates/item/elements?templateId="+defsID, body("rules", secondID, `{"sibling":true}`), elem.tag, token)
		expect(sibling, 201, "")
		url := "/api/v1/templates/item/definitions?templateId=" + defsID
		change := func(defs, tag string) reply {
			return call(http.MethodPatch, url, `{"elementId":"`+firstID+`","definitions":`+defs+`}`, tag, token)
		}
		const valid = `[{"name":"label","type":"number","defaultSource":" 9007199254740993 " }]`
		expect(change(`[{"name":"label","type":"string","defaultSource":"{"}]`, sibling.tag), 422, "invalid_definitions")
		expect(change(valid, ""), 428, "precondition_required")
		expect(change(valid, elem.tag), 412, "precondition_failed")
		expect(change(`[{"name":"label","type":"number","defaultSource":null}]`, sibling.tag), 400, "invalid_request")
		expect(change(`[{"name":"other","type":"number"}]`, sibling.tag), 422, "invalid_definitions")
		updated := change(valid, sibling.tag)
		expect(updated, 200, "")
		got := call(http.MethodGet, resource, "", "", token)
		if updated.tag == sibling.tag || got.tag != updated.tag || got.text != updated.text || !strings.Contains(got.text, `"defaultSource":" 9007199254740993 "`) || !strings.Contains(got.text, secondID) {
			t.Fatal("wrong F21 receipt", got)
		}
		required := change(`[{"name":"label","type":"number"}]`, got.tag)
		expect(required, 200, "")
		if strings.Contains(required.text, "defaultSource") {
			t.Fatal("default retained")
		}
		hook.beforeReplace <- func() {
			var rotateErr error
			issued, token, rotateErr = store.Owner(ctx, session, &externalaccess.Change{Version: issued.Version, Rotate: true})
			if rotateErr != nil {
				t.Error(rotateErr)
			}
		}
		expect(change(valid, required.tag), 401, "unauthorized")
		latest := call(http.MethodGet, resource, "", "", token)
		if latest.tag != required.tag || latest.text != required.text {
			t.Fatal("revoked change mutated draft")
		}
	})
	expect(add(body("outbounds", firstID, `{}`), first.tag), 409, "element_exists")
	expect(add(body("rules", secondID, `{}`), empty.tag), 412, "precondition_failed")
	expect(add(body("rules", secondID, `{"x":0,"x":1}`), first.tag), 422, "invalid_content")
	expect(add(body("exclude_ips", secondID, `{}`), first.tag), 400, "invalid_request")
	expect(add(`{"section":"rules","elementId":"`+secondID+`","source":null}`, first.tag), 400, "invalid_request")
	next := add(body("rules", secondID, `{"type":"field","outboundTag":"example"}`), first.tag)
	expect(next, 201, "")
	expect(call(http.MethodPatch, item, `{"elementId":"`+firstID+`","source":"{}","section":null}`, next.tag, token), 400, "invalid_request")
	version3 := version2
	if err := json.Unmarshal([]byte(next.text), &version3); err != nil || version3.Version != "3" || len(version3.Sections) != 1 || len(version3.Sections[0].Elements) != 2 ||
		version3.Sections[0].Elements[0].Source != source || version3.Sections[0].Elements[1].ID != secondID {
		t.Fatalf("second snapshot %+v %v", version3, err)
	}
	log := add(body("log", thirdID, `{"loglevel":"warning"}`), next.tag)
	expect(log, 201, "")
	expect(add(body("log", "550e8400-e29b-41d4-a716-446655440003", `{}`), log.tag), 409, "section_occupied")
	const extraID = "550e8400-e29b-41d4-a716-446655440003"
	addition := body("rules", extraID,
		`{"type":"field","outboundTag":"other.example"}`)
	patch, err := json.Marshal(map[string]string{"elementId": firstID, "source": `{"tag":"changed.example"}`})
	if err != nil {
		t.Fatal(err)
	}
	added, replaced := make(chan reply, 1), make(chan reply, 1)
	go func() { added <- add(addition, log.tag) }()
	go func() { replaced <- call(http.MethodPatch, item, string(patch), log.tag, token) }()
	addResult, patchResult := <-added, <-replaced
	if addResult.err != nil || patchResult.err != nil ||
		!(addResult.status == 201 && patchResult.status == 412 || addResult.status == 412 && patchResult.status == 200) {
		t.Fatalf("competing responses %+v %+v", addResult, patchResult)
	}
	winner := addResult
	if patchResult.status == 200 {
		winner = patchResult
	}
	rotate := make(chan error, 1)
	hook.beforeReplace <- func() {
		_, _, err := store.Owner(ctx, session, &externalaccess.Change{Version: issued.Version, Rotate: true})
		rotate <- err
	}
	expect(add(body("rules", "550e8400-e29b-41d4-a716-446655440004", `{}`), winner.tag), 401, "unauthorized")
	if err := <-rotate; err != nil {
		t.Fatal(err)
	}
	expect(add(body("rules", "550e8400-e29b-41d4-a716-446655440004", `{}`), winner.tag), 401, "unauthorized")
	final, err := templates.New(sqlite.NewTemplateRepository(db)).Get(ctx, " +&/ alpha ")
	rules := 2
	if addResult.status == 201 {
		rules = 3
	}
	if err != nil || final.Version != 5 || len(final.Sections) != 2 || len(final.Sections[0].Elements) != rules {
		t.Fatalf("final version %+v %v", final, err)
	}
}

type externalTemplateWriteHook struct {
	*sqlite.ExternalAccess
	beforeReplace chan func()
}

func (h externalTemplateWriteHook) ReplaceExternalTemplate(ctx context.Context, permit externalaccess.Permit, draft configuration.TemplateDraft, version int64) (int64, error) {
	select {
	case before := <-h.beforeReplace:
		before()
	default:
	}
	return h.ExternalAccess.ReplaceExternalTemplate(ctx, permit, draft, version)
}
