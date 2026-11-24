package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/agenthttp"
	"golang.org/x/sys/unix"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/desired"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/server"
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

func TestAgentDesiredTLSKeepsCacheAfterInterruptedBoundaryCatalog(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "server.sqlite")
	invitation, credentials := fixtureEnrollment(t, dbPath)
	key := agentcredential.Generate(agentcredential.Working).Encode()
	id, err := credentials.Enroll(ctx, invitation, key)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	publications := sqlite.NewPublications(db)
	handler := server.NewAgentHandler(credentials, publications)
	var truncate atomic.Bool
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !truncate.Load() {
			handler.ServeHTTP(w, r)
			return
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)
		for name, values := range recorder.Header() {
			w.Header()[name] = values
		}
		w.WriteHeader(recorder.Code)
		raw := recorder.Body.Bytes()
		if len(raw) != 0 {
			_, _ = w.Write(raw[:len(raw)-1])
		}
	}))
	defer tlsServer.Close()
	roots := x509.NewCertPool()
	roots.AddCert(tlsServer.Certificate())
	transport, err := agenthttp.New(tlsServer.URL, roots)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	dir := filepath.Join(t.TempDir(), "state")
	cache, err := statefile.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	binding := enrollment.State{Origin: tlsServer.URL, Key: key, RouterID: id.RouterID}
	first, err := desired.Sync(ctx, binding, cache, transport)
	if err != nil || first == nil || first.Number != 1 {
		t.Fatalf("initial fetch = %v, %v", first, err)
	}

	latest := fixtureDesiredCatalog("{}")
	remaining := agentrevision.MaxCatalog
	for _, file := range latest.Files[1:] {
		remaining -= len(file.Content)
	}
	payload := make([]byte, remaining)
	for i := range payload {
		payload[i] = ' '
	}
	copy(payload, `{"log":{}}`)
	latest.Files[0].Content = payload
	prev, err := publications.Read(ctx, publication.ReadRequest{RouterID: id.RouterID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publications.Commit(ctx, publication.CommitPlan{
		Expected: publication.Expectations{RouterID: id.RouterID, TemplateID: prev.TemplateID,
			AcceptedVersion: prev.Accepted.Version, ActiveGeneration: prev.Active.Generation, DesiredNumber: prev.Desired.Number},
		Settings: prev.Active.Draft, NewRevision: latest,
	}); err != nil {
		t.Fatal(err)
	}
	prior, err := cache.LoadDesired()
	if err != nil {
		t.Fatal(err)
	}
	truncate.Store(true)
	if failed, err := desired.Sync(ctx, binding, cache, transport); failed != nil || !errors.Is(err, enrollment.ErrTemporary) {
		t.Fatalf("interrupted verified TLS response = %v", err)
	}
	afterFailure, err := cache.LoadDesired()
	if err != nil || !reflect.DeepEqual(prior, afterFailure) {
		t.Fatalf("interrupted response replaced complete cache: %v", err)
	}
	truncate.Store(false)
	start := time.Now()
	got, err := desired.Sync(ctx, binding, cache, transport)
	if err != nil || got == nil || got.Number != 2 || !reflect.DeepEqual(got.Files, latest.Files) {
		t.Fatalf("retry at decoded size limit failed: %v", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := statefile.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if same, err := desired.Sync(ctx, binding, reopened, transport); err != nil || !reflect.DeepEqual(got, same) {
		t.Fatalf("conditional read after reopen failed: %v", err)
	}
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err == nil {
		t.Logf("8 MiB TLS fetch, durable write, and conditional reopen in %s, process maxrss=%d (%s)", time.Since(start), usage.Maxrss, runtime.GOOS)
	}
}
