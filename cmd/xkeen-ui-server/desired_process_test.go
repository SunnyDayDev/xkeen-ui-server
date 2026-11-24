package main

import (
	"bytes"
	"crypto/tls"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/agenthttp"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/desired"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func fixtureDesiredCatalog(log string) *publication.VerifiedFiles {
	return &publication.VerifiedFiles{Profile: configuration.XrayIdentityVersion, Files: []configuration.CatalogFile{
		{Name: "01_log.json", Content: []byte(log)}, {Name: "02_api.json", Content: []byte("{}")},
		{Name: "03_inbounds.json", Content: []byte(`{"inbounds":[]}`)}, {Name: "04_outbounds.json", Content: []byte(`{"outbounds":[]}`)},
		{Name: "05_routing.json", Content: []byte(`{"routing":{"rules":[]}}`)},
	}}
}

func TestProcessAgentFetchesDesiredAcrossRestart(t *testing.T) {
	ctx := t.Context()
	agent := buildAgentForProcessTest(t)
	dbPath := filepath.Join(t.TempDir(), "server.sqlite")
	invitation, credentials := fixtureEnrollment(t, dbPath)
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := sqlite.NewPublications(db)
	const firstID, secondID = "router-example", "second.example"
	secondCatalog := fixtureDesiredCatalog("{ }")
	if _, err := store.Commit(ctx, publication.CommitPlan{
		FirstConfirmation: true,
		Expected:          publication.Expectations{RouterID: secondID, TemplateID: "template-example", AcceptedVersion: 1},
		Settings:          routersettings.Draft{ID: secondID, TemplateID: "template-example"}, NewRevision: secondCatalog,
	}); err != nil {
		t.Fatal(err)
	}
	secondInvite, err := credentials.Issue(ctx, secondID, agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	secondKey := agentcredential.Generate(agentcredential.Working).Encode()
	if _, err := credentials.Enroll(ctx, secondInvite, secondKey); err != nil {
		t.Fatal(err)
	}

	certPath, keyPath, roots := fixtureServerCertificate(t)
	web := &http.Client{Timeout: time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}}
	defer web.CloseIdleConnections()
	startServer := func(address string) (*process, string) {
		t.Helper()
		p := startProcess(t, testBinary, "--db-path", dbPath, "--listen", address, "--tls-cert", certPath, "--tls-key", keyPath)
		return p, p.readyWithClient(t, web, "https")
	}
	server, address := startServer("127.0.0.1:0")
	origin := "https://" + address
	transport, err := agenthttp.New(origin, roots)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()

	dir := filepath.Join(t.TempDir(), "agent")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, statefile.InvitationName), []byte(invitation+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fetch := func(number int64, want *publication.VerifiedFiles) (string, os.FileInfo) {
		t.Helper()
		p := startProcess(t, agent, "--server", origin, "--state-dir", dir, "--ca-file", certPath)
		waitForAgentAuthentication(t, p)
		stopRouterAgent(t, p)
		state, err := statefile.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		cache, err := state.LoadDesired()
		if err != nil || cache == nil {
			t.Fatalf("missing complete desired cache: %v", err)
		}
		got, err := agentrevision.Decode(firstID, cache.Tag, cache.Body)
		if err != nil || got.Number != number || got.Profile != want.Profile || !reflect.DeepEqual(got.Files, want.Files) ||
			cache.RouterID != firstID || cache.Origin != origin {
			t.Fatalf("incorrect cached desired snapshot: %v", err)
		}
		info, err := os.Stat(filepath.Join(dir, statefile.DesiredName))
		if err != nil {
			t.Fatal(err)
		}
		return cache.Tag, info
	}

	firstTag, firstInfo := fetch(1, fixtureDesiredCatalog("{}\n"))
	got, err := transport.ReadDesired(ctx, secondKey, firstTag)
	second, decodeErr := agentrevision.Decode(secondID, got.Tag, got.Body)
	if err != nil || got.Kind != desired.Complete || decodeErr != nil || second.Number != 1 ||
		!bytes.Equal(second.Files[0].Content, secondCatalog.Files[0].Content) || got.Tag == firstTag {
		t.Fatal("foreign conditional suppressed or replaced the second device snapshot")
	}
	cachedTag, cachedInfo := fetch(1, fixtureDesiredCatalog("{}\n"))
	if firstTag != cachedTag || !os.SameFile(firstInfo, cachedInfo) {
		t.Fatal("matching restart rewrote the cached snapshot")
	}
	stopAgentProcess(t, server)

	var latest *publication.VerifiedFiles
	for i, log := range []string{"{  }", "{\n}\n"} {
		prev, err := store.Read(ctx, publication.ReadRequest{RouterID: firstID})
		if err != nil {
			t.Fatal(err)
		}
		latest = fixtureDesiredCatalog(log)
		if number, err := store.Commit(ctx, publication.CommitPlan{
			Expected: publication.Expectations{RouterID: firstID, TemplateID: prev.TemplateID,
				AcceptedVersion: prev.Accepted.Version, ActiveGeneration: prev.Active.Generation, DesiredNumber: prev.Desired.Number},
			Settings: prev.Active.Draft, NewRevision: latest,
		}); err != nil || number != int64(i+2) {
			t.Fatalf("publish = %d, %v", number, err)
		}
	}
	server, restarted := startServer(address)
	if restarted != address {
		t.Fatal("server did not preserve the configured origin")
	}
	if tag, _ := fetch(3, latest); tag == firstTag {
		t.Fatal("new publication retained the old strong tag")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM desired_router_revisions WHERE router_id=?`, secondID); err != nil {
		t.Fatal(err)
	}
	if got, err := transport.ReadDesired(ctx, secondKey, got.Tag); err != nil || got.Kind != desired.Absent {
		t.Fatalf("no desired after registered snapshot removal = %v, %v", got.Kind, err)
	}
	stopAgentProcess(t, server)
}
