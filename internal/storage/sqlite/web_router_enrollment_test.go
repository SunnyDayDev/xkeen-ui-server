package sqlite

import (
	"database/sql"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

func TestWebRouterEnrollmentMigrationKeepsPendingInvitation(t *testing.T) {
	ctx := t.Context()
	path := t.TempDir() + "/enrollment.sqlite"
	prior, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer prior.Close()
	if err := migrate(ctx, prior, migrations[:10]); err != nil {
		t.Fatal(err)
	}
	storeCurrentPublication(t, prior)
	invitation := agentcredential.Generate(agentcredential.Invitation)
	proof := invitation.Proof()
	now := time.Unix(1000, 0)
	if _, err := prior.ExecContext(ctx, `INSERT INTO agent_invitations
		(selector, router_id, purpose, expires_ns, secret_digest, expected_generation)
		VALUES (?, 'router', 1, ?, ?, 0)`, proof.Selector[:], now.Add(15*time.Minute).UnixNano(), proof.Digest[:]); err != nil {
		t.Fatal(err)
	}
	if err := prior.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := readText(t, db, "SELECT max(version) FROM schema_migrations"); got != strconv.Itoa(len(migrations)) {
		t.Fatalf("migration version = %s", got)
	}
	credentials := agentcredentials.NewService(NewAgentCredentials(db), fixedWebTime(now))
	if id, err := credentials.Enroll(ctx, invitation.Encode(), agentcredential.Generate(agentcredential.Working).Encode()); err != nil || id.RouterID != "router" {
		t.Fatalf("old invitation not preserved: %#v %v", id, err)
	}
}

func TestWebRouterEnrollmentReadTracksPendingExpiryAndRegistration(t *testing.T) {
	ctx := t.Context()
	db, _, digest, now := fixtureWebTemplateWriter(t)
	storeCurrentPublication(t, db)
	clock := func() time.Time { return now }
	enrollment := NewWebRouterEnrollment(db)
	read := func() webadmin.EnrollmentState {
		t.Helper()
		state, err := enrollment.Read(ctx, digest, clock, "router")
		if err != nil {
			t.Fatal(err)
		}
		if state.RouterID != "router" || state.Desired != 1 || state.Applied != 0 {
			t.Fatalf("unexpected enrollment read: %+v", state)
		}
		return state
	}
	if got := read(); got.Phase != "missing" || got.Epoch != 0 || !got.Expires.IsZero() {
		t.Fatalf("initial state: %+v", got)
	}
	credentials := agentcredentials.NewService(NewAgentCredentials(db), clock)
	invitation, err := credentials.Issue(ctx, "router", agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Phase != "pending" || got.Epoch != 1 || !got.Expires.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("pending state: %+v", got)
	}
	now = now.Add(15 * time.Minute)
	if got := read(); got.Phase != "expired" || got.Epoch != 1 {
		t.Fatalf("expiry boundary: %+v", got)
	}
	invitation, err = credentials.Issue(ctx, "router", agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	working := agentcredential.Generate(agentcredential.Working).Encode()
	if _, err := credentials.Enroll(ctx, invitation, working); err != nil {
		t.Fatal(err)
	}
	if _, err := credentials.Enroll(ctx, invitation, working); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Phase != "registered" || got.Epoch != 3 || !got.Expires.IsZero() || got.Contact != "unobserved" {
		t.Fatalf("registered state: %+v", got)
	}
	identity, err := credentials.Identity(ctx, working)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewContacts(db, clock).Accept(ctx, identity, nil); err != nil {
		t.Fatal(err)
	}
	now = now.Add(179 * time.Second)
	if online, err := enrollment.Read(ctx, digest, clock, "router"); err != nil || online.Contact != "online" || online.Applied != 0 {
		t.Fatalf("heartbeat conflated with apply: %+v %v", online, err)
	}
	now = now.Add(time.Second)
	if offline, err := enrollment.Read(ctx, digest, clock, "router"); err != nil || offline.Contact != "offline" || offline.Applied != 0 {
		t.Fatalf("offline boundary: %+v %v", offline, err)
	}
	if err := credentials.Revoke(ctx, "router"); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Phase != "revoked" || got.Epoch != 4 {
		t.Fatalf("revoked state: %+v", got)
	}
	if got, err := enrollment.Read(ctx, [32]byte{12}, clock, "router"); !errors.Is(err, webadmin.ErrSignedOut) || got.RouterID != "" {
		t.Fatalf("private state escaped: %+v %v", got, err)
	}
}

func TestWebRouterEnrollmentWriterRejectsStaleAndRevokedRequests(t *testing.T) {
	ctx := t.Context()
	db, _, digest, now := fixtureWebTemplateWriter(t)
	storeCurrentPublication(t, db)
	writer := NewWebRouterEnrollment(db)
	makeProof := func() agentcredential.Proof { return agentcredential.Generate(agentcredential.Invitation).Proof() }
	initial := webadmin.EnrollmentCommand{RouterID: "router", Desired: 1}
	request := initial
	request.Desired = 2
	if _, err := writer.Issue(ctx, digest, fixedWebTime(now), request, makeProof()); !errors.Is(err, webadmin.ErrEnrollmentConflict) {
		t.Fatalf("wrong revision: %v", err)
	}
	errs := make(chan error, 2)
	for range 2 {
		go func() { _, err := writer.Issue(ctx, digest, fixedWebTime(now), initial, makeProof()); errs <- err }()
	}
	one, two := <-errs, <-errs
	if !(one == nil && errors.Is(two, webadmin.ErrEnrollmentConflict) || two == nil && errors.Is(one, webadmin.ErrEnrollmentConflict)) {
		t.Fatalf("parallel issue: %v, %v", one, two)
	}
	if err := NewAgentCredentials(db).Revoke(ctx, "router"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Issue(ctx, digest, fixedWebTime(now.Add(20*time.Minute)), initial, makeProof()); !errors.Is(err, webadmin.ErrEnrollmentConflict) {
		t.Fatalf("ABA issued another token: %v", err)
	}
	request = initial
	request.Epoch = 2
	if _, err := writer.Issue(ctx, digest, fixedWebTime(now.Add(webadmin.IdleLifetime)), request, makeProof()); !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("failed command extended session: %v", err)
	}
	if got := readText(t, db, "SELECT count(*) FROM agent_invitations"); got != "0" {
		t.Fatalf("replayed invitation count=%s", got)
	}
}

func TestWebRouterEnrollmentRenewalRacesWithRedemption(t *testing.T) {
	ctx := t.Context()
	db, _, digest, now := fixtureWebTemplateWriter(t)
	storeCurrentPublication(t, db)
	clock := fixedWebTime(now)
	writer := NewWebRouterEnrollment(db)
	first := agentcredential.Generate(agentcredential.Invitation)
	initial := webadmin.EnrollmentCommand{RouterID: "router", Desired: 1}
	if got, err := writer.Issue(ctx, digest, clock, initial, first.Proof()); err != nil || !got.Expires.Equal(now.Add(agentcredentials.InvitationLifetime)) || got.Epoch != 1 {
		t.Fatalf("committed receipt: %+v %v", got, err)
	}
	replacement := agentcredential.Generate(agentcredential.Invitation)
	renewal := webadmin.EnrollmentCommand{RouterID: "router", Desired: 1, Epoch: 1, Replace: true}
	credentials := agentcredentials.NewService(NewAgentCredentials(db), clock)
	renewed, redeemed := make(chan error, 1), make(chan error, 1)
	go func() { _, err := writer.Issue(ctx, digest, clock, renewal, replacement.Proof()); renewed <- err }()
	go func() {
		_, err := credentials.Enroll(ctx, first.Encode(), agentcredential.Generate(agentcredential.Working).Encode())
		redeemed <- err
	}()
	issueErr, enrollErr := <-renewed, <-redeemed
	state, err := writer.Read(ctx, digest, clock, "router")
	if err != nil || state.Epoch != 2 {
		t.Fatalf("race state: %+v %v", state, err)
	}
	switch {
	case issueErr == nil && errors.Is(enrollErr, agentcredentials.ErrUnauthorized) && state.Phase == "pending":
	case enrollErr == nil && errors.Is(issueErr, webadmin.ErrEnrollmentConflict) && state.Phase == "registered":
	default:
		t.Fatalf("race outcome: issue=%v redeem=%v, %+v", issueErr, enrollErr, state)
	}
}
