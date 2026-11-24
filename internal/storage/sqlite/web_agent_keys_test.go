package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

func TestWebAgentKeysPreserveIdentityAfterRotationAndRevocation(t *testing.T) {
	ctx := t.Context()
	db, _, digest, now := fixtureWebTemplateWriter(t)
	storeCurrentPublication(t, db)
	clock := fixedWebTime(now)
	keys := agentcredentials.NewService(NewAgentCredentials(db), clock)
	initial, err := keys.Issue(ctx, "router", agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	old := agentcredential.Generate(agentcredential.Working).Encode()
	identity, err := keys.Enroll(ctx, initial, old)
	if err != nil {
		t.Fatal(err)
	}
	contacts := NewContacts(db, clock)
	if err := contacts.Accept(ctx, identity, &reporting.Report{Sequence: 5, Revision: 1, State: "applied"}); err != nil {
		t.Fatal(err)
	}
	var notified []string
	writer := NewWebRouterEnrollment(db, func(id string) { notified = append(notified, id) })
	command := func(action webadmin.AgentKeyAction, epoch int64, token agentcredential.Token) (webadmin.EnrollmentReceipt, error) {
		t.Helper()
		return writer.Manage(ctx, digest, clock, webadmin.AgentKeyCommand{RouterID: "router", Epoch: epoch, Desired: 1, Action: action}, token.Proof())
	}
	replacement := agentcredential.Generate(agentcredential.Invitation)
	receipt, err := command(webadmin.RotateAgentKey, 2, replacement)
	if err != nil || receipt.Epoch != 3 || !receipt.Expires.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("rotation receipt: %+v %v", receipt, err)
	}
	if _, err := keys.Identity(ctx, old); err != nil {
		t.Fatalf("rotating revoked early: %v", err)
	}
	if _, err := command(webadmin.RotateAgentKey, 2, agentcredential.Generate(agentcredential.Invitation)); !errors.Is(err, webadmin.ErrEnrollmentConflict) {
		t.Fatalf("stale issue: %v", err)
	}
	next := agentcredential.Generate(agentcredential.Working).Encode()
	second, err := keys.Enroll(ctx, replacement.Encode(), next)
	if err != nil || second.RouterID != identity.RouterID || second.Generation != identity.Generation+1 {
		t.Fatalf("new identity: %+v %v", second, err)
	}
	if _, err := keys.Identity(ctx, old); !errors.Is(err, agentcredentials.ErrUnauthorized) {
		t.Fatalf("old working key: %v", err)
	}
	if seq, err := contacts.Cursor(ctx, second); err != nil || seq != 5 {
		t.Fatalf("lost cursor %v %v", seq, err)
	}
	pending := agentcredential.Generate(agentcredential.Invitation)
	if _, err := command(webadmin.RotateAgentKey, 4, pending); err != nil {
		t.Fatal(err)
	}
	if receipt, err := command(webadmin.RevokeAgentKey, 5, agentcredential.Token{}); err != nil || receipt.Epoch != 6 || !receipt.Expires.IsZero() {
		t.Fatalf("revoke receipt: %+v %v", receipt, err)
	}
	if len(notified) != 1 || notified[0] != "router" {
		t.Fatalf("revocation notification: %v", notified)
	}
	if _, err := keys.Identity(ctx, next); !errors.Is(err, agentcredentials.ErrUnauthorized) {
		t.Fatalf("revoked working key: %v", err)
	}
	if _, err := keys.Enroll(ctx, pending.Encode(), agentcredential.Generate(agentcredential.Working).Encode()); !errors.Is(err, agentcredentials.ErrUnauthorized) {
		t.Fatalf("revoked pending invitation: %v", err)
	}
	recovery := agentcredential.Generate(agentcredential.Invitation)
	if _, err := command(webadmin.RotateAgentKey, 6, recovery); !errors.Is(err, webadmin.ErrEnrollmentConflict) {
		t.Fatalf("rotation was also recovery: %v", err)
	}
	if receipt, err := command(webadmin.RecoverAgentKey, 6, recovery); err != nil || receipt.Epoch != 7 {
		t.Fatalf("recovery issuance: %+v %v", receipt, err)
	}
	third, err := keys.Enroll(ctx, recovery.Encode(), agentcredential.Generate(agentcredential.Working).Encode())
	if err != nil || third.RouterID != identity.RouterID {
		t.Fatalf("recovered identity: %+v %v", third, err)
	}
	if seq, err := contacts.Cursor(ctx, third); err != nil || seq != 5 {
		t.Fatalf("recovery lost cursor %v %v", seq, err)
	}
	if state, err := contacts.Status(ctx, third.RouterID); err != nil || state.Applied != 1 || state.Desired != 1 {
		t.Fatalf("recovery lost publication: %+v %v", state, err)
	}
}

func TestWebAgentKeysRevocationRacesWithRedemption(t *testing.T) {
	ctx := t.Context()
	db, _, digest, now := fixtureWebTemplateWriter(t)
	storeCurrentPublication(t, db)
	clock := fixedWebTime(now)
	keys := agentcredentials.NewService(NewAgentCredentials(db), clock)
	first, err := keys.Issue(ctx, "router", agentcredentials.Initial)
	if err != nil {
		t.Fatal(err)
	}
	old := agentcredential.Generate(agentcredential.Working).Encode()
	if _, err := keys.Enroll(ctx, first, old); err != nil {
		t.Fatal(err)
	}
	writer := NewWebRouterEnrollment(db)
	token := agentcredential.Generate(agentcredential.Invitation)
	if _, err := writer.Manage(ctx, digest, clock, webadmin.AgentKeyCommand{RouterID: "router", Desired: 1, Epoch: 2, Action: webadmin.RotateAgentKey}, token.Proof()); err != nil {
		t.Fatal(err)
	}
	next := agentcredential.Generate(agentcredential.Working).Encode()
	redeemed, revoked := make(chan error, 1), make(chan error, 1)
	go func() { _, err := keys.Enroll(ctx, token.Encode(), next); redeemed <- err }()
	go func() {
		_, err := writer.Manage(ctx, digest, clock, webadmin.AgentKeyCommand{RouterID: "router", Desired: 1, Epoch: 3, Action: webadmin.RevokeAgentKey}, agentcredential.Proof{})
		revoked <- err
	}()
	redeemErr, revokeErr := <-redeemed, <-revoked
	if _, err := keys.Identity(ctx, old); !errors.Is(err, agentcredentials.ErrUnauthorized) {
		t.Fatalf("old working key survived: %v", err)
	}
	switch {
	case redeemErr == nil && errors.Is(revokeErr, webadmin.ErrEnrollmentConflict):
		if _, err := keys.Identity(ctx, next); err != nil {
			t.Fatalf("replacement lost authority: %v", err)
		}
	case revokeErr == nil && errors.Is(redeemErr, agentcredentials.ErrUnauthorized):
		if _, err := keys.Identity(ctx, next); !errors.Is(err, agentcredentials.ErrUnauthorized) {
			t.Fatalf("revoke left new authority: %v", err)
		}
	default:
		t.Fatalf("inconsistent race: %v %v", redeemErr, revokeErr)
	}
}
