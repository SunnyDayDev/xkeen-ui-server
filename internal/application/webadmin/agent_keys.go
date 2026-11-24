package webadmin

import (
	"context"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
)

type AgentKeyAction string

const (
	RotateAgentKey  AgentKeyAction = "rotate"
	RecoverAgentKey AgentKeyAction = "recover"
	RevokeAgentKey  AgentKeyAction = "revoke"
)

type AgentKeyCommand struct {
	RouterID       string
	Epoch, Desired int64
	Action         AgentKeyAction
}

type AgentKeyStore interface {
	Manage(context.Context, [32]byte, func() time.Time, AgentKeyCommand, agentcredential.Proof) (EnrollmentReceipt, error)
}

type AgentKeys struct {
	store AgentKeyStore
	now   func() time.Time
}

func NewAgentKeys(store AgentKeyStore, now func() time.Time) AgentKeys {
	return AgentKeys{store: store, now: now}
}

func (s AgentKeys) Manage(ctx context.Context, token, proof string, command AgentKeyCommand) (EnrollmentReceipt, error) {
	digest, _, err := SessionProof(token, proof, true)
	if err != nil {
		return EnrollmentReceipt{}, err
	}
	if command.Action == RevokeAgentKey {
		return s.store.Manage(ctx, digest, s.now, command, agentcredential.Proof{})
	}
	invitation := agentcredential.Generate(agentcredential.Invitation)
	receipt, err := s.store.Manage(ctx, digest, s.now, command, invitation.Proof())
	if err != nil {
		return EnrollmentReceipt{}, err
	}
	receipt.Token = invitation.Encode()
	return receipt, nil
}
