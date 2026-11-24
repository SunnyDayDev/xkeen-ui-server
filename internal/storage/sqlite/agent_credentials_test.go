package sqlite

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
)

func TestCredentialMigrationPreservesPublication(t *testing.T) {
	ctx := t.Context()
	path := t.TempDir() + "/migration.sqlite"
	prior, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer prior.Close()
	if err := migrate(ctx, prior, migrations[:5]); err != nil {
		t.Fatal(err)
	}
	storeCurrentPublication(t, prior)
	before := readText(t, prior, "SELECT hex(files) FROM desired_router_revisions WHERE router_id='router' AND revision=1")
	if err := prior.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := readText(t, db, "SELECT hex(files) FROM desired_router_revisions WHERE router_id='router' AND revision=1"); got != before {
		t.Fatal("credential migration changed desired bytes")
	}
	if got := readText(t, db, "SELECT count(*) FROM agent_registrations"); got != "0" {
		t.Fatalf("migrated registrations = %s", got)
	}
}

func TestAgentCredentialIssueRequiresReadyRouterAndExactExpiry(t *testing.T) {
	db, _ := openPublicationDB(t)
	now := time.Unix(1000, 0)
	service := agentcredentials.NewService(NewAgentCredentials(db), func() time.Time { return now })
	ctx := t.Context()
	if token, err := service.Issue(ctx, "router", agentcredentials.Initial); token != "" || !errors.Is(err, agentcredentials.ErrRouterNotReady) {
		t.Fatalf("unprepared issue = %q, %v", token, err)
	}
	storeCurrentPublication(t, db)
	if token, err := service.Issue(ctx, "router", agentcredentials.Replacement); token != "" || !errors.Is(err, agentcredentials.ErrInvalidTransition) {
		t.Fatalf("initial replacement = %q, %v", token, err)
	}
	first, err := service.Issue(ctx, "router", agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Issue(ctx, "router", agentcredentials.Initial)
	if err != nil || first == second {
		t.Fatalf("new invitation: %v", err)
	}
	working := agentcredential.Generate(agentcredential.Working).Encode()
	assertUnauthorized := func(token, candidate string) {
		t.Helper()
		if _, err := service.Enroll(ctx, token, candidate); !errors.Is(err, agentcredentials.ErrUnauthorized) {
			t.Fatalf("unexpected enrollment error: %v", err)
		}
	}
	assertUnauthorized(first, working)
	assertUnauthorized(second, first)
	now = now.Add(15 * time.Minute)
	assertUnauthorized(second, working)
}

func TestAgentCredentialEnrollReplayRotationAndRevocation(t *testing.T) {
	ctx := t.Context()
	db, path := openPublicationDB(t)
	storeCurrentPublication(t, db)
	other, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	one := agentcredentials.NewService(NewAgentCredentials(db), clock)
	two := agentcredentials.NewService(NewAgentCredentials(other), clock)
	invite, err := one.Issue(ctx, "router", agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	first := agentcredential.Generate(agentcredential.Working).Encode()
	second := agentcredential.Generate(agentcredential.Working).Encode()
	firstIdentity, err := one.Enroll(ctx, invite, first)
	if err != nil || firstIdentity.RouterID != "router" || firstIdentity.Generation != 1 {
		t.Fatalf("first enrollment = %#v, %v", firstIdentity, err)
	}
	if replay, err := two.Enroll(ctx, invite, first); err != nil || replay != firstIdentity {
		t.Fatalf("exact replay = %#v, %v", replay, err)
	}
	if _, err := two.Enroll(ctx, invite, second); !errors.Is(err, agentcredentials.ErrUnauthorized) {
		t.Fatalf("other candidate = %v", err)
	}
	for _, query := range []string{
		"SELECT quote(secret_digest) FROM agent_invitations",
		"SELECT quote(secret_digest) FROM agent_working_keys",
	} {
		stored := readText(t, db, query)
		if strings.Contains(stored, invite) || strings.Contains(stored, first) || !strings.HasPrefix(stored, "X'") || len(stored) != 67 {
			t.Fatal("expected only a 32-byte digest")
		}
	}
	replacement, err := two.Issue(ctx, "router", agentcredentials.Replacement)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := one.Identity(ctx, first); err != nil || id != firstIdentity {
		t.Fatalf("old key before replacement = %#v, %v", id, err)
	}
	if _, err := two.Enroll(ctx, replacement, first); !errors.Is(err, agentcredentials.ErrUnauthorized) {
		t.Fatalf("reused old working key = %v", err)
	}
	id, err := two.Enroll(ctx, replacement, second)
	if err != nil || id.RouterID != "router" || id.Generation <= firstIdentity.Generation {
		t.Fatalf("replacement = %#v, %v", id, err)
	}
	if _, err := one.Identity(ctx, first); !errors.Is(err, agentcredentials.ErrUnauthorized) {
		t.Fatalf("old key = %v", err)
	}
	if _, err := one.Enroll(ctx, invite, first); !errors.Is(err, agentcredentials.ErrUnauthorized) {
		t.Fatalf("prior replay = %v", err)
	}
	pending, err := one.Issue(ctx, "router", agentcredentials.Replacement)
	if err != nil {
		t.Fatal(err)
	}
	if err := two.Revoke(ctx, "router"); err != nil {
		t.Fatal(err)
	}
	if _, err := one.Identity(ctx, second); !errors.Is(err, agentcredentials.ErrUnauthorized) {
		t.Fatalf("revoked key = %v", err)
	}
	third := agentcredential.Generate(agentcredential.Working).Encode()
	if _, err := one.Enroll(ctx, pending, third); !errors.Is(err, agentcredentials.ErrUnauthorized) {
		t.Fatalf("stale invitation = %v", err)
	}
	recovery, err := two.Issue(ctx, "router", agentcredentials.Replacement)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := one.Enroll(ctx, recovery, third)
	if err != nil || recovered.RouterID != "router" || recovered.Generation <= id.Generation {
		t.Fatalf("recovery = %#v, %v", recovered, err)
	}
}

func TestAgentCredentialConcurrentEnrollmentAndRollback(t *testing.T) {
	ctx := t.Context()
	db, path := openPublicationDB(t)
	storeCurrentPublication(t, db)
	secondDB, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer secondDB.Close()
	services := []*agentcredentials.Service{
		agentcredentials.NewService(NewAgentCredentials(db), time.Now),
		agentcredentials.NewService(NewAgentCredentials(secondDB), time.Now),
	}
	invite, err := services[0].Issue(ctx, "router", agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{
		agentcredential.Generate(agentcredential.Working).Encode(),
		agentcredential.Generate(agentcredential.Working).Encode(),
	}
	execSQL(t, db, `CREATE TRIGGER reject_invitation_consume BEFORE UPDATE ON agent_invitations
		BEGIN SELECT RAISE(FAIL, 'intentionally rejected test write'); END`)
	if _, err := services[0].Enroll(ctx, invite, keys[0]); !errors.Is(err, agentcredentials.ErrUnavailable) {
		t.Fatalf("failed transaction = %v", err)
	}
	if _, err := services[1].Identity(ctx, keys[0]); !errors.Is(err, agentcredentials.ErrUnauthorized) {
		t.Fatalf("partial enrollment = %v", err)
	}
	execSQL(t, db, "DROP TRIGGER reject_invitation_consume")
	signal := make(chan struct{})
	type outcome struct {
		index int
		err   error
	}
	outcomes := make(chan outcome, len(services))
	for i, service := range services {
		go func() {
			<-signal
			_, err := service.Enroll(ctx, invite, keys[i])
			outcomes <- outcome{i, err}
		}()
	}
	close(signal)
	first, second := <-outcomes, <-outcomes
	if first.err != nil {
		first, second = second, first
	}
	if first.err != nil || !errors.Is(second.err, agentcredentials.ErrUnauthorized) {
		t.Fatalf("expected one winner: %v, %v", first.err, second.err)
	}
	if got, err := services[second.index].Identity(ctx, keys[first.index]); err != nil || got.RouterID != "router" {
		t.Fatalf("winner identity = %#v, %v", got, err)
	}
}

func TestAgentCredentialRevokeDoesNotClaimSuccessWhenWriteIsIgnored(t *testing.T) {
	ctx := t.Context()
	db, _ := openPublicationDB(t)
	storeCurrentPublication(t, db)
	service := agentcredentials.NewService(NewAgentCredentials(db), time.Now)
	invite, err := service.Issue(ctx, "router", agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	key := agentcredential.Generate(agentcredential.Working).Encode()
	if _, err := service.Enroll(ctx, invite, key); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, `CREATE TRIGGER ignore_revocation BEFORE UPDATE ON agent_registrations
		BEGIN SELECT RAISE(IGNORE); END`)
	if err := service.Revoke(ctx, "router"); !errors.Is(err, agentcredentials.ErrUnavailable) {
		t.Fatalf("ignored revocation reported %v", err)
	}
}
