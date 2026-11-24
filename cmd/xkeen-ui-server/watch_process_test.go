package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/desired"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/revisionwatch"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/server"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

type observedWatchReader struct {
	publications *sqlite.Publications
	observed     chan string
}

func (r observedWatchReader) ReadWatch(ctx context.Context, id agentcredentials.Identity) (int64, error) {
	number, err := r.publications.ReadWatch(ctx, id)
	select {
	case r.observed <- id.RouterID:
	default:
	}
	return number, err
}

func TestProcessRouterAgentWatchReadsCommittedChanges(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	agent := buildAgentForProcessTest(t)
	dbPath := filepath.Join(t.TempDir(), "server.sqlite")
	firstInvite, _ := fixtureEnrollment(t, dbPath)
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	observed := make(chan string, 16)
	watch := revisionwatch.New(ctx, observedWatchReader{sqlite.NewPublications(db), observed}, 25*time.Second, 4)
	store := sqlite.NewPublications(db, watch.Notify)
	credentials := agentcredentials.NewService(sqlite.NewAgentCredentials(db, watch.Notify), time.Now)
	second := routersettings.Draft{ID: "second.example", TemplateID: "template-example"}
	if _, err := store.Commit(ctx, publication.CommitPlan{FirstConfirmation: true, Expected: publication.Expectations{RouterID: second.ID, TemplateID: second.TemplateID, AcceptedVersion: 1}, Settings: second, NewRevision: fixtureDesiredCatalog("{ }")}); err != nil {
		t.Fatal(err)
	}
	secondInvite, err := credentials.Issue(ctx, second.ID, agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	tlsServer := httptest.NewTLSServer(server.NewAgentHandler(credentials, store, sqlite.NewContacts(db, time.Now), watch))
	defer tlsServer.Close()
	defer cancel()
	cert := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsServer.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	start := func(invite string) (*process, string) {
		dir := filepath.Join(t.TempDir(), "state")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, statefile.InvitationName), []byte(invite), 0600); err != nil {
			t.Fatal(err)
		}
		p := startProcess(t, agent, "--server", tlsServer.URL, "--state-dir", dir, "--ca-file", cert)
		waitForAgentAuthentication(t, p)
		return p, dir
	}
	first, firstDir := start(firstInvite)
	next, secondDir := start(secondInvite)
	firstID := "router-example"
	waiting := make(map[string]bool)
	timeout := time.After(5 * time.Second)
	for len(waiting) < 2 {
		select {
		case id := <-observed:
			waiting[id] = true
		case <-timeout:
			t.Fatal("agents did not establish both watches")
		}
	}
	candidate := fixtureDesiredCatalog("{\n}\n")
	inputs, err := store.Read(ctx, publication.ReadRequest{RouterID: firstID})
	if err != nil {
		t.Fatal(err)
	}
	nextRevision, err := store.Commit(ctx, publication.CommitPlan{
		Expected: publication.Expectations{RouterID: firstID, TemplateID: inputs.TemplateID, AcceptedVersion: inputs.Accepted.Version, ActiveGeneration: inputs.Active.Generation, DesiredNumber: inputs.Desired.Number},
		Settings: inputs.Active.Draft, NewRevision: candidate,
	})
	if err != nil || nextRevision != 2 {
		t.Fatalf("publish = %d, %v", nextRevision, err)
	}
	got := awaitDesiredCopy(t, firstDir, 2)
	if !reflect.DeepEqual(got.Files, candidate.Files) {
		t.Fatal("agent did not receive exact committed files")
	}
	if got := awaitDesiredCopy(t, secondDir, 1); got.Number != 1 {
		t.Fatal("unrelated device changed")
	}
	if err := credentials.Revoke(ctx, firstID); err != nil {
		t.Fatal(err)
	}
	if err := first.wait(t); err == nil {
		t.Fatal("revoked waiter did not stop")
	}
	stopRouterAgent(t, next)
}

func awaitDesiredCopy(t *testing.T, dir string, number int64) *agentrevision.Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(filepath.Join(dir, statefile.DesiredName))
		var envelope struct{ Entry desired.Entry }
		if err == nil && json.Unmarshal(raw, &envelope) == nil {
			snapshot, err := agentrevision.Decode(envelope.Entry.RouterID, envelope.Entry.Tag, envelope.Entry.Body)
			if err == nil && snapshot.Number == number {
				return snapshot
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no complete desired revision %d", number)
	return nil
}
