package restart

import (
	"bytes"
	"encoding/json"
	"net"
	"reflect"
	"slices"
	"strconv"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/jsonvalue"
)

// RuleEvidence comes from the owning supervisor under its startup barrier.
// Observed is the complete ordered RoutingService projection. ProbeOutcome
// records a new SOCKS request paired with a successful direct control request.
type RuleEvidence struct {
	Runtime preparation.Runtime
	// Controlled requires exclusive ownership of both startup and API writes.
	Controlled                     bool
	AllowPort, DenyPort, ProbePort uint16
	SOCKS, API                     string
	Observed                       []RuleRoute
	ProbeOutcome                   string
}

type RuleRoute struct{ Tag, Outbound string }
type RulePlan struct {
	Expected []RuleRoute
	Outcome  string
}

// ApplicationPlan with neither field set uses the standard restart.
type ApplicationPlan struct {
	Unchanged bool
	Rules     *RulePlan
}

func (intent Intent) Plan(current *Confirmed, proof RuleEvidence) ApplicationPlan {
	if current == nil || !reflect.DeepEqual(current.Baseline, intent.Previous) {
		return ApplicationPlan{}
	}
	if reflect.DeepEqual(intent.Previous.Files, intent.TargetFiles) {
		return ApplicationPlan{Unchanged: true}
	}
	return ApplicationPlan{Rules: ClassifyRuleChange(intent.Target.Snapshot.Profile, intent.Previous, intent.TargetFiles, proof)}
}

// The verified split configuration has a single SOCKS inbound, two terminal
// outbounds and a dedicated API listener. Keep the supported topology narrow
// until another topology has an independent reproducible traffic witness.
var hotTopology = []struct{ name, document string }{
	{"01_log.json", `{"log":{"loglevel":"warning"}}`},
	{"02_api.json", `{"api":{"tag":"api","listen":"127.0.0.1:10085","services":["RoutingService","StatsService"]},"stats":{}}`},
	{"03_inbounds.json", `{"inbounds":[{"tag":"lab-socks","listen":"127.0.0.1","port":1080,"protocol":"socks","settings":{"auth":"noauth","udp":false}}]}`},
	{"04_outbounds.json", `{"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"block","protocol":"blackhole"}]}`},
}

// ClassifyRuleChange runs only after the owning application service has
// authenticated the desired revision, observed the baseline and locally
// validated the exact target. A nil plan selects the ordinary restart.
func ClassifyRuleChange(profile string, previous preparation.Baseline, next []preparation.BaselineFile, proof RuleEvidence) *RulePlan {
	if profile != configuration.XrayIdentityVersion || !proof.Controlled || proof.Runtime != previous.Runtime || proof.Runtime.PID <= 0 || proof.Runtime.StartTick == 0 || proof.Runtime.VerifierID == "" || proof.Runtime.BootID == "" || proof.Runtime.DirectoryInode == 0 {
		return nil
	}
	if len(previous.Files) != len(hotTopology)+1 || len(next) != len(previous.Files) {
		return nil
	}
	inboundTag := ""
	for i, section := range hotTopology {
		old, new := previous.Files[i], next[i]
		if old.Name != section.name || !reflect.DeepEqual(old, new) {
			return nil
		}
		parsed, err := jsonvalue.Parse(old.Content)
		want, _ := jsonvalue.Parse([]byte(section.document))
		if err != nil || !stripTopologyMarkers(parsed) {
			return nil
		}
		doc, _ := parsed.(map[string]any)
		switch section.name {
		case "02_api.json":
			api, _ := doc["api"].(map[string]any)
			if !loopbackEndpoint(proof.API) || api["listen"] != proof.API {
				return nil
			}
			api["listen"] = "127.0.0.1:10085"
		case "03_inbounds.json":
			list, _ := doc["inbounds"].([]any)
			if len(list) != 1 {
				return nil
			}
			obj, _ := list[0].(map[string]any)
			tag, _ := obj["tag"].(string)
			port, _ := obj["port"].(json.Number)
			if tag == "" || !loopbackEndpoint(proof.SOCKS) || net.JoinHostPort("127.0.0.1", string(port)) != proof.SOCKS {
				return nil
			}
			inboundTag = tag
			obj["tag"] = "lab-socks"
			obj["port"] = json.Number("1080")
		}
		if !reflect.DeepEqual(parsed, want) {
			return nil
		}
	}
	i := len(hotTopology)
	a, b := previous.Files[i], next[i]
	if a.Name != "05_routing.json" || b.Name != a.Name || a.UID != b.UID || a.GID != b.GID || a.Mode != b.Mode || bytes.Equal(a.Content, b.Content) {
		return nil
	}
	old, updated := ruleProjection(a.Content, inboundTag), ruleProjection(b.Content, inboundTag)
	if old.marker != updated.marker {
		return nil
	}
	if proof.AllowPort == 0 || proof.DenyPort == 0 || proof.ProbePort == 0 || proof.AllowPort == proof.DenyPort || proof.ProbePort == proof.AllowPort || proof.ProbePort == proof.DenyPort {
		return nil
	}
	if len(old.routes) == 0 || len(updated.routes) == 0 || !slices.Equal(old.routes, proof.Observed) {
		return nil
	}
	for _, set := range []ruleSet{old, updated} {
		if set.outcome(proof.AllowPort) != "direct" || set.outcome(proof.DenyPort) != "block" {
			return nil
		}
	}
	outcome := updated.outcome(proof.ProbePort)
	if old.outcome(proof.ProbePort) != proof.ProbeOutcome || outcome == proof.ProbeOutcome {
		return nil
	}
	return &RulePlan{Expected: updated.routes, Outcome: outcome}
}

type ruleSet struct {
	marker string
	routes []RuleRoute
	ports  []uint16
}

func (s ruleSet) outcome(port uint16) string {
	for i, condition := range s.ports {
		if condition == 0 || condition == port {
			return s.routes[i].Outbound
		}
	}
	return "direct" // first outbound in the verified topology
}

func ruleProjection(raw []byte, inboundTag string) ruleSet {
	value, issue := jsonvalue.Parse(raw)
	if issue != nil {
		return ruleSet{}
	}
	doc, _ := value.(map[string]any)
	routing, _ := doc["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	marker, valid := takeMarker(routing)
	if !valid {
		return ruleSet{}
	}
	if len(doc) != 1 || len(routing) != 2 || routing["domainStrategy"] != "AsIs" || len(rules) == 0 {
		return ruleSet{}
	}
	seen := make(map[string]bool, len(rules))
	out := ruleSet{marker: marker}
	for _, item := range rules {
		rule, _ := item.(map[string]any)
		tag, _ := rule["ruleTag"].(string)
		target, _ := rule["outboundTag"].(string)
		if _, valid := takeMarker(rule); !valid {
			return ruleSet{}
		}
		var port uint64
		if raw, exists := rule["port"]; exists {
			value, ok := raw.(string)
			n, err := strconv.ParseUint(value, 10, 16)
			if !ok || err != nil || n == 0 || strconv.FormatUint(n, 10) != value {
				return ruleSet{}
			}
			port = n
			delete(rule, "port")
		}
		if len(rule) != 4 || rule["type"] != "field" || !reflect.DeepEqual(rule["inboundTag"], []any{inboundTag}) || tag == "" || seen[tag] || (target != "direct" && target != "block") {
			return ruleSet{}
		}
		seen[tag] = true
		out.routes = append(out.routes, RuleRoute{tag, target})
		out.ports = append(out.ports, uint16(port))
	}
	return out
}

// B12 projects identity only at these documented root positions. All bytes
// in these files have already compared equal between generations.
func stripTopologyMarkers(value any) bool {
	doc, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for _, k := range []string{"log", "stats"} {
		if obj, ok := doc[k].(map[string]any); ok {
			if _, valid := takeMarker(obj); !valid {
				return false
			}
		}
	}
	for _, k := range []string{"inbounds", "outbounds"} {
		entries, _ := doc[k].([]any)
		for _, e := range entries {
			obj, _ := e.(map[string]any)
			if _, valid := takeMarker(obj); !valid {
				return false
			}
		}
	}
	return true
}
func takeMarker(obj map[string]any) (string, bool) {
	raw, exists := obj["_xkeenUiId"]
	if !exists {
		return "", true
	}
	value, ok := raw.(string)
	if !ok || !configuration.ValidElementID(value) {
		return "", false
	}
	delete(obj, "_xkeenUiId")
	return value, true
}

func loopbackEndpoint(endpoint string) bool {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil || host != "127.0.0.1" {
		return false
	}
	n, err := strconv.ParseUint(port, 10, 16)
	return err == nil && n > 0 && strconv.FormatUint(n, 10) == port
}
