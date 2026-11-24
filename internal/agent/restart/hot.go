package restart

import (
	"context"
	"encoding/hex"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"slices"
)

const (
	HotDispatch Phase = "hot_dispatch"
	HotVerified Phase = "hot_verified"
	HotFiles    Phase = "hot_files"
)

// HotReceipt binds protected launcher provenance, the complete expected rule
// projection and behavioral witness to the generation recorded by its owner.
type HotReceipt struct {
	AllowPort, DenyPort, ProbePort uint16
	Guard                          string
	Expected                       []RuleRoute
	Outcome                        string
}

type hotWorking interface {
	LaunchGuard(preparation.Runtime) string
	RuleEvidence(context.Context, *Confirmed) RuleEvidence
	InstallHot(context.Context, Intent, func(Phase, *preparation.Runtime) error) (preparation.Baseline, error)
}

func launchGuard(working any, rt preparation.Runtime) string {
	if hot, ok := working.(hotWorking); ok {
		return hot.LaunchGuard(rt)
	}
	return ""
}

func (h *HotReceipt) Matches(e RuleEvidence) bool {
	return h != nil && e.Controlled && h.AllowPort == e.AllowPort && h.DenyPort == e.DenyPort && h.ProbePort == e.ProbePort && h.Outcome == e.ProbeOutcome && slices.Equal(h.Expected, e.Observed)
}

func (h *HotReceipt) valid() bool {
	if h == nil {
		return true
	}
	digest, err := hex.DecodeString(h.Guard)
	if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != h.Guard || h.AllowPort == 0 || h.DenyPort == 0 || h.ProbePort == 0 || h.AllowPort == h.DenyPort || h.AllowPort == h.ProbePort || h.DenyPort == h.ProbePort || len(h.Expected) == 0 || h.Outcome != "direct" && h.Outcome != "block" {
		return false
	}
	tags := make(map[string]bool)
	for _, r := range h.Expected {
		if r.Tag == "" || tags[r.Tag] || r.Outbound != "direct" && r.Outbound != "block" {
			return false
		}
		tags[r.Tag] = true
	}
	return true
}
