package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crypto/tls"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/agenthttp"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/contact"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
	"net/http"
)

func TestProcessContactQueueAndCursor(t *testing.T) {
	ctx := t.Context()
	database := filepath.Join(t.TempDir(), "server.sqlite")
	invite, credentials := fixtureEnrollment(t, database)
	cert, key, roots := fixtureServerCertificate(t)
	health := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	defer health.CloseIdleConnections()
	p := startProcess(t, testBinary, "--listen", "127.0.0.1:0", "--db-path", database, "--tls-cert", cert, "--tls-key", key)
	origin := "https://" + p.readyWithClient(t, health, "https")
	client, err := agenthttp.New(origin, roots)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	binding := enrollment.State{Origin: origin, RouterID: "router-example", Key: agentcredential.Generate(agentcredential.Working).Encode()}
	if _, err := client.Enroll(ctx, invite, binding.Key); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "agent")
	local, err := statefile.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	durable := contact.New(binding, local, client)
	if err := durable.Enqueue(ctx, reporting.Report{Revision: 1, State: "applied"}); err != nil {
		t.Fatal(err)
	}
	// Deliver from the transport without acknowledging it in the local queue.
	queued, err := local.LoadReport()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Contact(ctx, binding.Key, queued.Pending); err != nil {
		t.Fatal(err)
	}
	stopAgentProcess(t, p)
	p = startProcess(t, testBinary, "--listen", strings.TrimPrefix(origin, "https://"), "--db-path", database, "--tls-cert", cert, "--tls-key", key)
	p.readyWithClient(t, health, "https")
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	local, err = statefile.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	durable = contact.New(binding, local, client)
	if err := durable.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := client.Cursor(ctx, binding.Key); err != nil || got != 1 {
		t.Fatalf("cursor %d: %v", got, err)
	}
	if err := client.Contact(ctx, binding.Key, nil); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	status, err := sqlite.NewContacts(db, time.Now).Status(ctx, binding.RouterID)
	if err != nil || status.Presence != reporting.Online || status.Applied != 1 || status.Desired != 1 {
		t.Fatalf("status %#v: %v", status, err)
	}
	if err := credentials.Revoke(ctx, binding.RouterID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Cursor(ctx, binding.Key); !errors.Is(err, enrollment.ErrUnauthorized) {
		t.Fatalf("revoked cursor: %v", err)
	}
	stopAgentProcess(t, p)
}
