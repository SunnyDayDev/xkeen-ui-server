package restart

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

func hotFixture(t *testing.T) ([]preparation.BaselineFile, RuleEvidence) {
	t.Helper()
	names := []string{"01_log.json", "02_api.json", "03_inbounds.json", "04_outbounds.json", "05_routing.json"}
	var old []preparation.BaselineFile
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join("../../../lab/configs", name))
		if err != nil {
			t.Fatal(err)
		}
		old = append(old, preparation.BaselineFile{Name: name, Content: raw})
	}
	old[4].Content = []byte(`{"routing":{"domainStrategy":"AsIs","rules":[{"ruleTag":"deny","type":"field","inboundTag":["lab-socks"],"port":"18082","outboundTag":"block"},{"ruleTag":"switch","type":"field","inboundTag":["lab-socks"],"port":"18080","outboundTag":"direct"}]}}`)
	runtime := preparation.Runtime{PID: 42, StartTick: 7, VerifierID: "verified", BootID: "boot", DirectoryDevice: 1, DirectoryInode: 2}
	evidence := RuleEvidence{Runtime: runtime, Controlled: true, SOCKS: "127.0.0.1:1080", API: "127.0.0.1:10085", AllowPort: 18081, DenyPort: 18082, ProbePort: 18080, Observed: []RuleRoute{{"deny", "block"}, {"switch", "direct"}}, ProbeOutcome: "direct"}
	return old, evidence
}
func TestRuleClassification(t *testing.T) {
	old, evidence := hotFixture(t)
	runtime := evidence.Runtime
	for _, tc := range []struct {
		name     string
		file     int
		from, to string
		want     bool
	}{
		{"duplicate tag", 4, "switch", "deny", false},
		{"shadowed target", 4, "18080", "18082", false},
		{"changes fixed allow", 4, "18080", "18081", false},
		{"unsupported port syntax", 4, "18080", "18080,18081", false},
		{"nonrules marker", 4, `"rules":`, `"_xkeenUiId":"00000000-0000-4000-8000-000000000001","rules":`, false},
		{"unknown root", 4, `"routing":`, `"new":1,"routing":`, false},
		{"compatible", 4, "direct", "block", true}, {"same", 4, "direct", "direct", false}, {"strategy", 4, "AsIs", "IPIfNonMatch", false}, {"other file", 2, "1080", "1081", false}, {"unobserved", 4, "direct", "other", false}, {"unknown matcher", 4, `"outboundTag":"direct"`, `"port":80,"outboundTag":"block"`, false}, {"empty tag", 4, "switch", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := append([]preparation.BaselineFile(nil), old...)
			next[tc.file].Content = bytes.ReplaceAll(next[tc.file].Content, []byte(tc.from), []byte(tc.to))
			b := preparation.Baseline{Runtime: runtime, Files: old}
			if got := ClassifyRuleChange("26.3.27", b, next, evidence); (got != nil) != tc.want {
				t.Fatalf("plan %#v", got)
			}
			if tc.want {
				intent := Intent{Previous: b, TargetFiles: next, Target: Desired{Snapshot: agentrevision.Snapshot{Profile: "26.3.27"}}}
				if intent.Plan(nil, evidence).Rules != nil || intent.Plan(&Confirmed{}, evidence).Rules != nil || intent.Plan(&Confirmed{Baseline: b}, evidence).Rules == nil {
					t.Fatal("trusted current ownership")
				}
				extra := append(append([]preparation.BaselineFile(nil), next...), preparation.BaselineFile{Name: "06_extra.json", Content: []byte(`{"routing":{}}`)})
				if ClassifyRuleChange("26.3.27", b, extra, evidence) != nil {
					t.Fatal("additional routing source")
				}
				for _, mutate := range []func(*RuleEvidence){func(p *RuleEvidence) { p.Runtime.StartTick++ }, func(p *RuleEvidence) { p.Observed = nil }, func(p *RuleEvidence) { p.ProbePort = p.AllowPort }, func(p *RuleEvidence) { p.Controlled = false }} {
					stale := evidence
					mutate(&stale)
					if ClassifyRuleChange("26.3.27", b, next, stale) != nil {
						t.Fatal("stale proof")
					}
				}

				for _, bad := range []RuleEvidence{{}, {Runtime: runtime, Controlled: true}, {Runtime: runtime, Controlled: true, SOCKS: evidence.SOCKS, API: evidence.API, Observed: evidence.Observed, ProbeOutcome: "block"}} {
					if ClassifyRuleChange("26.3.27", b, next, bad) != nil {
						t.Fatal("incomplete proof")
					}
				}
				if ClassifyRuleChange("other", b, next, evidence) != nil {
					t.Fatal("unproved profile")
				}
			}
		})
	}
}
