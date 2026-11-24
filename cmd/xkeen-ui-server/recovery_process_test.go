package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/desired"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/revisionwatch"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/server"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

const recoveryRouter = "router-example"

type recoveryFixture struct {
	agent, dir, origin, cert string
	clock                    *atomic.Int64
	store                    *sqlite.Publications
	contacts                 *sqlite.Contacts
	credentials              *agentcredentials.Service
}

func newRecoveryFixture(t *testing.T, fault func(http.ResponseWriter, *http.Request) bool, wrap ...func(http.Handler) http.Handler) *recoveryFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	dbPath := filepath.Join(t.TempDir(), "server.sqlite")
	invitation, _ := fixtureEnrollment(t, dbPath)
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clock := new(atomic.Int64)
	clock.Store(time.Now().Unix())
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	watch := revisionwatch.New(ctx, sqlite.NewPublications(db), 25*time.Second, 4)
	store := sqlite.NewPublications(db, watch.Notify)
	contacts := sqlite.NewContacts(db, now)
	credentials := agentcredentials.NewService(sqlite.NewAgentCredentials(db, watch.Notify), now)
	handler := server.NewAgentHandler(credentials, store, contacts, watch)
	for _, apply := range wrap {
		handler = apply(handler)
	}
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fault(w, r) {
			handler.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(func() { cancel(); endpoint.Close() })
	cert := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: endpoint.Certificate().Raw})
	if err := os.WriteFile(cert, pemBytes, 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, statefile.InvitationName), []byte(invitation), 0600); err != nil {
		t.Fatal(err)
	}
	return &recoveryFixture{buildAgentForProcessTest(t), dir, endpoint.URL, cert, clock, store, contacts, credentials}
}

func (f *recoveryFixture) launch(t *testing.T) *process {
	t.Helper()
	return startProcess(t, f.agent, "--server", f.origin, "--state-dir", f.dir, "--ca-file", f.cert)
}

func (f *recoveryFixture) publish(t *testing.T, number int64, config string) *publication.VerifiedFiles {
	t.Helper()
	ctx := t.Context()
	current, err := f.store.Read(ctx, publication.ReadRequest{RouterID: recoveryRouter})
	if err != nil {
		t.Fatal(err)
	}
	files := fixtureDesiredCatalog(config)
	got, err := f.store.Commit(ctx, publication.CommitPlan{
		Expected: publication.Expectations{RouterID: recoveryRouter, TemplateID: current.TemplateID, AcceptedVersion: current.Accepted.Version,
			ActiveGeneration: current.Active.Generation, DesiredNumber: current.Desired.Number},
		Settings: current.Active.Draft, NewRevision: files,
	})
	if err != nil || got != number {
		t.Fatalf("publish revision %d: %v", got, err)
	}
	return files
}

func (f *recoveryFixture) status(t *testing.T) reporting.Status {
	t.Helper()
	status, err := f.contacts.Status(t.Context(), recoveryRouter)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func (f *recoveryFixture) cache(t *testing.T) (desired.Entry, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.dir, statefile.DesiredName))
	var record struct{ Entry desired.Entry }
	if err != nil || json.Unmarshal(raw, &record) != nil || record.Entry.RouterID != recoveryRouter {
		t.Fatal("missing verified cache")
	}
	return record.Entry, raw
}

func waitRecovery(t *testing.T, limit time.Duration, reason string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(reason)
}

func unavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = fmt.Fprint(w, `{"code":"unavailable"}`)
}

func TestProcessRouterAgentRecoveryPeriodicWithoutWatch(t *testing.T) {
	if os.Getenv("XKEEN_TEST_RECOVERY_MINUTE") != "1" {
		t.Skip("set XKEEN_TEST_RECOVERY_MINUTE=1 for the real 60-second interval")
	}
	var watches, reads atomic.Int64
	f := newRecoveryFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/agent/v1/watch":
			watches.Add(1)
			unavailable(w)
			return true
		case "/agent/v1/desired":
			reads.Add(1)
		}
		return false
	})
	p := f.launch(t)
	waitForAgentAuthentication(t, p)
	awaitDesiredCopy(t, f.dir, 1)
	waitRecovery(t, 5*time.Second, "watch did not fail", func() bool { return watches.Load() > 0 })
	f.publish(t, 2, "{ }")
	latest := f.publish(t, 3, "{\n}\n")
	waitRecovery(t, 75*time.Second, "periodic read did not run without watch", func() bool { return reads.Load() >= 2 })
	if current := awaitDesiredCopy(t, f.dir, 3); !reflect.DeepEqual(current.Files, latest.Files) {
		t.Fatal("periodic recovery changed the committed bytes")
	}
	status := f.status(t)
	if status.Presence != reporting.Online || status.Desired != 3 || status.Applied != 0 || status.Last != nil {
		t.Fatal("receiving a desired revision changed application status")
	}
	stopRouterAgent(t, p)
}

func TestProcessRouterAgentRecoveryOfflineRestart(t *testing.T) {
	const (
		healthy = iota
		offline
		truncated
		stale
		liveWatch
	)
	var mode atomic.Int32
	var disconnects, invalidReads, staleReads, hints, revokedWatches atomic.Int64
	var prior desired.Entry
	var latestTag string
	var latestBody []byte
	f := newRecoveryFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		phase := mode.Load()
		if phase == offline {
			conn, _, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error("could not interrupt fixture connection")
				unavailable(w)
				return true
			}
			_ = conn.Close()
			disconnects.Add(1)
			return true
		}
		switch {
		case r.URL.Path == "/agent/v1/watch" && phase == stale:
			if hints.Add(1) <= 2 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"kind":"desired_changed"}`)
				return true
			}
		case r.URL.Path == "/agent/v1/desired" && phase == truncated:
			invalidReads.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", strconv.Itoa(len(latestBody)))
			w.Header().Set("ETag", latestTag)
			_, _ = w.Write(latestBody[:len(latestBody)/2])
			return true
		case r.URL.Path == "/agent/v1/desired" && phase == stale:
			staleReads.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("ETag", prior.Tag)
			_, _ = w.Write(prior.Body)
			return true
		}
		if r.URL.Path == "/agent/v1/watch" {
			if phase != liveWatch {
				unavailable(w)
				return true
			}
			revokedWatches.Add(1)
		}
		return false
	})
	initial := f.launch(t)
	waitForAgentAuthentication(t, initial)
	awaitDesiredCopy(t, f.dir, 1)
	waitRecovery(t, 5*time.Second, "initial contact absent", func() bool { return f.status(t).Presence == reporting.Online })
	firstContact := f.status(t).LastContact
	first := readAgentStateWhileRunning(t, f.dir)
	old, oldBytes := f.cache(t)
	prior = old
	mode.Store(offline)
	waitRecovery(t, 5*time.Second, "fixture did not interrupt a TLS connection", func() bool { return disconnects.Load() > 0 })
	if err := initial.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := initial.wait(t); err == nil {
		t.Fatal("expected interrupted process")
	}
	f.publish(t, 2, "{ }")
	latest := f.publish(t, 3, "{\n}\n")
	f.clock.Store(firstContact.Add(179 * time.Second).Unix())
	if got := f.status(t); got.Presence != reporting.Online || got.Desired != 3 {
		t.Fatal("wrong 179-second status")
	}
	f.clock.Store(firstContact.Add(180 * time.Second).Unix())
	if got := f.status(t); got.Presence != reporting.Offline || got.Applied != 0 || got.Last != nil {
		t.Fatal("wrong offline/application status")
	}
	if err := os.Remove(filepath.Join(f.dir, statefile.InvitationName)); err != nil {
		t.Fatal(err)
	}
	var err error
	latestTag, latestBody, err = agentrevision.Encode(recoveryRouter, agentrevision.Snapshot{Number: 3, Profile: latest.Profile, Files: latest.Files})
	if err != nil {
		t.Fatal(err)
	}
	mode.Store(truncated)
	recovered := f.launch(t)
	waitRecovery(t, 5*time.Second, "agent did not retry the truncated response", func() bool { return invalidReads.Load() >= 2 })
	if _, raw := f.cache(t); !reflect.DeepEqual(raw, oldBytes) {
		t.Fatal("partial delivery advanced the candidate")
	}
	mode.Store(healthy)
	waitForAgentAuthentication(t, recovered)
	if current := awaitDesiredCopy(t, f.dir, 3); !reflect.DeepEqual(current.Files, latest.Files) {
		t.Fatal("restart did not recover exact latest bytes")
	}
	if actual := readAgentStateWhileRunning(t, f.dir); actual != first {
		t.Fatal("restart changed binding")
	}
	waitRecovery(t, 5*time.Second, "recovered contact absent", func() bool {
		current := f.status(t)
		return current.Presence == reporting.Online && current.LastContact.After(firstContact)
	})
	_, latestBytes := f.cache(t)
	mode.Store(stale)
	waitRecovery(t, 10*time.Second, "repeated hints did not trigger guarded reads", func() bool { return staleReads.Load() >= 2 && hints.Load() >= 3 })
	if _, raw := f.cache(t); !reflect.DeepEqual(raw, latestBytes) {
		t.Fatal("stale response changed the verified copy")
	}
	if got := f.status(t); got.Desired != 3 || got.Applied != 0 || got.Last != nil {
		t.Fatal("recovery promoted the application status")
	}
	if err := f.credentials.Revoke(t.Context(), recoveryRouter); err != nil {
		t.Fatal(err)
	}
	mode.Store(liveWatch)
	waitRecovery(t, 35*time.Second, "agent did not recheck revoked authorization after backoff", func() bool { return revokedWatches.Load() > 0 })
	if err := recovered.wait(t); err == nil || !strings.Contains(recovered.output(), enrollment.ErrUnauthorized.Error()) {
		t.Fatal("revocation did not stop the agent")
	}
	if _, raw := f.cache(t); !reflect.DeepEqual(raw, latestBytes) {
		t.Fatal("revocation changed the verified copy")
	}
}

func readAgentStateWhileRunning(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, statefile.StateName))
	if err != nil {
		t.Fatal("missing binding")
	}
	return string(raw)
}
