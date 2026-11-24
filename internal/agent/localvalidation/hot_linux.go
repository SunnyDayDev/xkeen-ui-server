//go:build linux

package localvalidation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/restart"
	"golang.org/x/sys/unix"
)

// HotConfig is provisioned by the trusted isolated direct launcher.
type HotConfig struct{ API, ProbeURL, ProbeMarker, FirewallSHA256 string }

func (w *RestartWorking) policy(ctx context.Context) string {
	c := w.hot
	if c == nil || !loopbackHost(c.API) || !strings.HasPrefix(c.API, "127.0.0.1:") || len(c.ProbeMarker) == 0 || len(c.ProbeMarker) > 256 {
		return ""
	}
	probe := localTarget(c.ProbeURL)
	if probe == nil {
		return ""
	}
	expected, ok := decodeDigest(c.FirewallSHA256)
	if !ok {
		return ""
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	loopback := false
	for _, link := range interfaces {
		if link.Flags&net.FlagUp != 0 {
			if link.Flags&net.FlagLoopback == 0 {
				return ""
			}
			loopback = true
		}
	}
	if !loopback {
		return ""
	}
	ns, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return ""
	}
	// The appliance root filesystem and extension libraries must be read-only.
	path, err := filepath.EvalSymlinks("/opt/sbin/iptables")
	if err != nil {
		return ""
	}
	var fs unix.Statfs_t
	if unix.Statfs(path, &fs) != nil || fs.Flags&unix.ST_RDONLY == 0 {
		return ""
	}
	f, err := openRegular(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, io.LimitReader(f, 8<<20))
	if err != nil || size == 8<<20 || hex.EncodeToString(h.Sum(nil)) != hex.EncodeToString(expected[:]) {
		return ""
	}
	raw, err := hotCommand(ctx, f, "iptables", nil, "-S", "OUTPUT")
	if err != nil {
		return ""
	}
	_, port, _ := net.SplitHostPort(c.API)
	first := "-A OUTPUT -d 127.0.0.1/32 -p tcp -m tcp --dport " + port + " -m owner ! --uid-owner 0 -j REJECT --reject-with icmp-port-unreachable"
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "-P OUTPUT ") || lines[1] != first {
		return ""
	}
	identity, _ := json.Marshal([]string{c.API, w.initial.SOCKS, w.initial.AllowURL, w.initial.AllowMarker, w.initial.DenyURL, w.initial.DenyMarker, c.ProbeURL, c.ProbeMarker, c.FirewallSHA256, hex.EncodeToString(w.initial.BinaryDigest[:]), strconv.FormatUint(uint64(w.uid), 10), strconv.FormatUint(uint64(w.gid), 10)})
	return fmt.Sprintf("%x:%x", sha256.Sum256(identity), sha256.Sum256([]byte(w.bootID+" "+ns)))
}

func hotCommand(ctx context.Context, binary *os.File, name string, extra *os.File, args ...string) ([]byte, error) {
	timeout, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(timeout, "/proc/self/fd/3", args...)
	cmd.Args[0] = name
	cmd.Env = []string{"PATH=/opt/sbin:/opt/bin:/usr/sbin:/usr/bin:/bin"}
	cmd.Dir = "/"
	cmd.ExtraFiles = []*os.File{binary}
	if extra != nil {
		cmd.ExtraFiles = append(cmd.ExtraFiles, extra)
	}
	var out boundedDiagnostic
	cmd.Stdout = &out
	cmd.WaitDelay = time.Second
	if cmd.Run() != nil || timeout.Err() != nil || out.truncated {
		return nil, restart.ErrEffects
	}
	return out.data.Bytes(), nil
}

func (w *RestartWorking) LaunchGuard(rt preparation.Runtime) string {
	if rt == w.guardedRuntime {
		return w.guardedPolicy
	}
	return ""
}

func (w *RestartWorking) RuleEvidence(ctx context.Context, current *restart.Confirmed) restart.RuleEvidence {
	if current == nil || current.Guard == "" || w.policy(ctx) != current.Guard {
		return restart.RuleEvidence{}
	}
	rt := current.Baseline.Runtime
	localNS, err := os.Readlink("/proc/self/ns/net")
	otherNS, otherErr := os.Readlink(fmt.Sprintf("/proc/%d/ns/net", rt.PID))
	if err != nil || otherErr != nil || localNS != otherNS || !w.matchesReceiptDirectory(rt) || !ownsListener(rt.PID, w.hot.API) {
		return restart.RuleEvidence{}
	}
	p := w.initial
	p.PID, p.StartTick = rt.PID, rt.StartTick
	if !w.sameDirectory() || !p.identifyProcess() || !ownsListener(rt.PID, p.SOCKS) || !p.checkTraffic(ctx) {
		return restart.RuleEvidence{}
	}
	binary, err := w.workingBinary()
	if err != nil {
		return restart.RuleEvidence{}
	}
	defer binary.Close()
	raw, err := hotCommand(ctx, binary, "xray", nil, "api", "lsrules", "--server="+w.hot.API, "--timeout=2")
	if err != nil {
		return restart.RuleEvidence{}
	}
	var listed struct {
		Rules []struct {
			RuleTag string
			Tag     string
		}
	}
	if json.Unmarshal(raw, &listed) != nil || len(listed.Rules) == 0 {
		return restart.RuleEvidence{}
	}
	evidence := restart.RuleEvidence{Runtime: rt, SOCKS: p.SOCKS, API: w.hot.API}
	for _, r := range listed.Rules {
		evidence.Observed = append(evidence.Observed, restart.RuleRoute{Tag: r.RuleTag, Outbound: r.Tag})
	}
	targets := []string{p.AllowURL, p.DenyURL, w.hot.ProbeURL}
	ports := []*uint16{&evidence.AllowPort, &evidence.DenyPort, &evidence.ProbePort}
	for i, raw := range targets {
		u := localTarget(raw)
		if u == nil {
			return restart.RuleEvidence{}
		}
		value, err := strconv.ParseUint(u.Port(), 10, 16)
		if err != nil {
			return restart.RuleEvidence{}
		}
		*ports[i] = uint16(value)
	}
	target := localTarget(w.hot.ProbeURL)
	direct := probeClient(nil)
	defer direct.CloseIdleConnections()
	proxy := probeClient(http.ProxyURL(&url.URL{Scheme: "socks5", Host: p.SOCKS}))
	defer proxy.CloseIdleConnections()
	if probe(ctx, direct, target, w.hot.ProbeMarker) != probeOK {
		return restart.RuleEvidence{}
	}
	switch probe(ctx, proxy, target, w.hot.ProbeMarker) {
	case probeOK:
		evidence.ProbeOutcome = "direct"
	case probeFailed:
		evidence.ProbeOutcome = "block"
	default:
		return restart.RuleEvidence{}
	}
	if probe(ctx, direct, target, w.hot.ProbeMarker) != probeOK || !p.checkTraffic(ctx) || !p.identifyProcess() || !ownsListener(rt.PID, w.hot.API) || w.policy(ctx) != current.Guard {
		return restart.RuleEvidence{}
	}
	evidence.Controlled = true
	return evidence
}

func (w *RestartWorking) InstallHot(ctx context.Context, intent restart.Intent, checkpoint func(restart.Phase, *preparation.Runtime) error) (preparation.Baseline, error) {
	fail := func() (preparation.Baseline, error) { return preparation.Baseline{}, restart.ErrEffects }
	if intent.Hot == nil || !w.exactFiles(intent.Previous.Files) {
		return fail()
	}
	current := &restart.Confirmed{Baseline: intent.Previous, Guard: intent.Hot.Guard}
	evidence := w.RuleEvidence(ctx, current)
	plan := intent.Plan(current, evidence)
	if plan.Rules == nil || plan.Rules.Outcome != intent.Hot.Outcome || !reflect.DeepEqual(plan.Rules.Expected, intent.Hot.Expected) {
		return fail()
	}
	target := intent.TargetFiles[len(intent.TargetFiles)-1]
	if target.Name != "05_routing.json" {
		return fail()
	}
	fd, err := unix.MemfdCreate("routing.json", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return fail()
	}
	file := os.NewFile(uintptr(fd), "routing.json")
	defer file.Close()
	if _, err = file.Write(target.Content); err != nil {
		return fail()
	}
	if _, err = unix.FcntlInt(file.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); err != nil {
		return fail()
	}
	binary, err := w.workingBinary()
	if err != nil {
		return fail()
	}
	defer binary.Close()
	if _, err := hotCommand(ctx, binary, "xray", file, "api", "adrules", "--server="+w.hot.API, "--timeout=2", "--append=false", "/proc/self/fd/4"); err != nil {
		return fail()
	}
	if !intent.Hot.Matches(w.RuleEvidence(ctx, current)) || !w.exactFiles(intent.Previous.Files) {
		return fail()
	}
	if err := checkpoint(restart.HotVerified, nil); err != nil {
		return preparation.Baseline{}, err
	}
	dir, err := w.openDirectory()
	if err != nil {
		return fail()
	}
	defer dir.Close()
	if !putFile(int(dir.Fd()), target) || dir.Sync() != nil {
		return fail()
	}
	current.Baseline.Files = intent.TargetFiles
	current.Hot = intent.Hot
	result, err := w.Observe(ctx, current)
	if err != nil || !reflect.DeepEqual(result, current.Baseline) {
		return fail()
	}
	if err := checkpoint(restart.HotFiles, &result.Runtime); err != nil {
		return preparation.Baseline{}, err
	}
	return result, nil
}
