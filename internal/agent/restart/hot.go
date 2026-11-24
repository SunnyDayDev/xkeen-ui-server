package restart

import (
	"context"
	"encoding/hex"
	"slices"
	"strings"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/jsonvalue"
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
	if !validGuard(h.Guard) || h.AllowPort == 0 || h.DenyPort == 0 || h.ProbePort == 0 || h.AllowPort == h.DenyPort || h.AllowPort == h.ProbePort || h.DenyPort == h.ProbePort || len(h.Expected) == 0 || h.Outcome != "direct" && h.Outcome != "block" {
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

func (h *HotReceipt) agrees(files []preparation.BaselineFile) bool {
	if h == nil {
		return true
	}
	if !h.valid() || len(files) != 5 || files[2].Name != "03_inbounds.json" || files[4].Name != "05_routing.json" {
		return false
	}
	value, err := jsonvalue.Parse(files[2].Content)
	if err != nil {
		return false
	}
	doc, _ := value.(map[string]any)
	list, _ := doc["inbounds"].([]any)
	if len(list) != 1 {
		return false
	}
	in, _ := list[0].(map[string]any)
	tag, _ := in["tag"].(string)
	if tag == "" {
		return false
	}
	set := ruleProjection(files[4].Content, tag)
	return slices.Equal(set.routes, h.Expected) && set.outcome(h.AllowPort) == "direct" && set.outcome(h.DenyPort) == "block" && set.outcome(h.ProbePort) == h.Outcome
}

// Rebound authorizes the same witness policy on a newly launched, independently
// verified runtime. Existing processes must still match the entire guard.
func (h *HotReceipt) Rebound(next string) *HotReceipt {
	if h == nil || !validGuard(h.Guard) || !validGuard(next) {
		return nil
	}
	oldProfile, _, _ := strings.Cut(h.Guard, ":")
	newProfile, _, _ := strings.Cut(next, ":")
	if oldProfile != newProfile {
		return nil
	}
	copy := *h
	copy.Guard = next
	return &copy
}

func validGuard(value string) bool {
	profile, epoch, ok := strings.Cut(value, ":")
	if !ok {
		return false
	}
	for _, part := range []string{profile, epoch} {
		digest, err := hex.DecodeString(part)
		if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != part {
			return false
		}
	}
	return true
}
