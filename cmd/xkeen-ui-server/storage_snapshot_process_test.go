package main

import (
	"bytes"
	"crypto/tls"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func TestProcessStorageBackupCapturesCommittedWALAndProtectsDestinations(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	primary, backup, temp := filepath.Join(dir, "active.sqlite"), filepath.Join(dir, "snapshot.sqlite"), filepath.Join(dir, "untrusted.sqlite")
	db, err := sqlite.Open(ctx, primary)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	service := templates.New(sqlite.NewTemplateRepository(db))
	if _, err := service.Create(ctx, configuration.TemplateDraft{ID: "before-backup.example"}); err != nil {
		t.Fatal(err)
	}
	if stat, err := os.Stat(primary + "-wal"); err != nil || stat.Size() < 4096 {
		t.Fatal("fixture has no committed WAL")
	}
	if _, errout, code := runBinary(t, "storage", "backup", "--db-path", primary, "--output", backup); code != 0 {
		t.Fatalf("backup failed (%d): %s", code, errout)
	}
	if info, err := os.Stat(backup); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("snapshot is not private")
	}
	snapshot := func() []byte {
		t.Helper()
		contents, err := os.ReadFile(backup)
		if err != nil {
			t.Fatal(err)
		}
		return contents
	}
	checked := snapshot()
	if _, err := service.Create(ctx, configuration.TemplateDraft{ID: "after-backup.example"}); err != nil {
		t.Fatal(err)
	}
	if _, _, code := runBinary(t, "storage", "backup", "--db-path", primary, "--output", backup); code == 0 || !bytes.Equal(checked, snapshot()) {
		t.Fatal("second backup replaced the original snapshot")
	}
	if _, _, code := runBinary(t, "storage", "restore", "--backup", backup, "--output", primary); code == 0 {
		t.Fatal("restore replaced the active newer database")
	}
	if _, err := service.Get(ctx, "after-backup.example"); err != nil {
		t.Fatal("restore failure lost later data")
	}
	if err := os.WriteFile(temp, []byte("synthetic corrupt database"), 0600); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(dir, "restored.sqlite")
	if _, _, code := runBinary(t, "storage", "restore", "--backup", temp, "--output", restored); code == 0 {
		t.Fatal("corrupt backup accepted")
	}
	if _, err := os.Lstat(restored); !os.IsNotExist(err) {
		t.Fatal("invalid backup published a file")
	}
	if _, errout, code := runBinary(t, "storage", "restore", "--backup", backup, "--output", restored); code != 0 {
		t.Fatalf("restore failed: %s", errout)
	}
	read, err := sqlite.OpenExisting(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	restoredTemplates := templates.New(sqlite.NewTemplateRepository(read))
	if _, err := restoredTemplates.Get(ctx, "before-backup.example"); err != nil {
		t.Fatal("committed WAL content missing")
	}
	if _, err := restoredTemplates.Get(ctx, "after-backup.example"); err == nil {
		t.Fatal("snapshot included data written afterward")
	}
}

func TestProcessStorageRestoreAuthorityAndReturnToNewerDatabase(t *testing.T) {
	const authority = "admin.example"
	const previous = "synthetic previous password"
	const current = "synthetic current password"
	dir := t.TempDir()
	path := filepath.Join(dir, "server.sqlite")
	backup := filepath.Join(dir, "backup.sqlite")
	restored := filepath.Join(dir, "restored.sqlite")
	password := filepath.Join(dir, "password")
	invitation, credentials := fixtureEnrollment(t, path)
	working := agentcredential.Generate(agentcredential.Working).Encode()
	if _, err := credentials.Enroll(t.Context(), invitation, working); err != nil {
		t.Fatal(err)
	}
	command := func(args ...string) {
		t.Helper()
		if _, stderr, code := runBinary(t, args...); code != 0 {
			t.Fatalf("command %s failed (%d): %s", args[0], code, stderr)
		}
	}
	admin := func(action, secret string) {
		t.Helper()
		if err := os.WriteFile(password, []byte(secret+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		command("admin", action, "--db-path", path, "--password-file", password)
	}
	admin("setup", previous)

	cert, key, roots := fixtureServerCertificate(t)
	client := &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	defer client.CloseIdleConnections()
	var endpoint string
	launch := func(database string) *process {
		t.Helper()
		p := startProcess(t, testBinary, "--listen", "127.0.0.1:0", "--db-path", database, "--tls-cert", cert, "--tls-key", key, "--web-origin", "https://"+authority)
		endpoint = "https://" + p.readyWithClient(t, client, "https")
		return p
	}
	web := func(method, route, body string, cookie *http.Cookie, want int) *http.Cookie {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, endpoint+"/ui/v1"+route, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = authority
		if body != "" {
			req.Header.Set("Origin", "https://"+authority)
			req.Header.Set("Content-Type", "application/json")
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s %s returned %d, want %d", method, route, response.StatusCode, want)
		}
		if cookies := response.Cookies(); len(cookies) == 1 {
			return cookies[0]
		}
		return nil
	}
	report := func(sequence string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), "POST", endpoint+"/agent/v1/contact", strings.NewReader(`{"report":{"sequence":"`+sequence+`","revision":"1","state":"applied"}}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+working)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("report returned %d", response.StatusCode)
		}
	}
	cursor := func(want string) {
		t.Helper()
		status, result := requestAgentJSON(t, client, "GET", endpoint+"/agent/v1/contact", working, "")
		if status != http.StatusOK || result["sequence"] != want {
			t.Fatalf("cursor returned %d, sequence %q", status, result["sequence"])
		}
	}
	login := func(secret string, want int) *http.Cookie {
		t.Helper()
		cookie := web("POST", "/login", `{"password":"`+secret+`"}`, nil, want)
		if want == http.StatusOK && cookie == nil {
			t.Fatal("administrator session missing")
		}
		return cookie
	}
	active := launch(path)
	oldCookie := login(previous, http.StatusOK)
	report("5")
	command("storage", "backup", "--db-path", path, "--output", backup)
	report("6")
	stopRouterAgent(t, active)
	admin("reset", current)
	command("storage", "restore", "--backup", backup, "--output", restored)

	isolated := launch(restored)
	web("GET", "/templates", "", oldCookie, http.StatusOK)
	login(previous, http.StatusOK)
	cursor("5")
	login(current, http.StatusUnauthorized)
	stopRouterAgent(t, isolated)

	active = launch(path)
	defer stopRouterAgent(t, active)
	web("GET", "/templates", "", oldCookie, http.StatusUnauthorized)
	login(current, http.StatusOK)
	cursor("6")
}
