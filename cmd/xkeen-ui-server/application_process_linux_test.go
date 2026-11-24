//go:build linux

package main

import (
	"bytes"
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
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/localvalidation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/restart"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestProcessAgentAppliesTwoControlledGenerations(t *testing.T) {
	checkControlledApplication(t, "apply")
}

func TestProcessAgentAppliesGuardedHotRules(t *testing.T) {
	if os.Getenv("XKEEN_TEST_HOT") != "1" {
		t.Skip("isolated hot application lab required")
	}
	checkControlledApplication(t, "hot")
}
func TestProcessAgentRollbackOutcomes(t *testing.T) {
	checkControlledApplication(t, "rollback")
}

func TestProcessAgentRollbackPreservesForeignWorkingData(t *testing.T) {
	checkControlledApplication(t, "conflict")
}

func TestProcessAgentRecoversInterruptedApplication(t *testing.T) {
	checkControlledApplication(t, "startup-recovery")
}

func checkControlledApplication(t *testing.T, mode string) {
	t.Helper()
	binary, xray, cgroup := os.Getenv("XKEEN_TEST_PREPARATION_BINARY"), os.Getenv("XKEEN_TEST_XRAY"), os.Getenv("XKEEN_TEST_CGROUP")
	if binary == "" || xray == "" || cgroup == "" || os.Geteuid() != 0 {
		t.Skip("requires disposable delegated Entware application profile")
	}
	f := newRecoveryFixture(t, func(http.ResponseWriter, *http.Request) bool { return false })
	const agentUID, runtimeUID, workerUID = 65532, 65533, 65534
	base, err := os.MkdirTemp("", "e03-process-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	if err := os.Chmod(base, 0755); err != nil {
		t.Fatal(err)
	}
	path := func(name string) string { return filepath.Join(base, name) }
	for _, name := range []string{"work", "agent", "stage", "socket", "journal"} {
		mode := os.FileMode(0700)
		if name == "work" || name == "socket" {
			mode = 0755
		}
		if err := os.Mkdir(path(name), mode); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite := func(path string, data []byte, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	for from, to := range map[string]string{
		filepath.Join(f.dir, statefile.InvitationName): "agent/invitation",
		f.cert: "trusted-ca.pem",
	} {
		raw, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(path(to), raw, 0600)
		if to == "trusted-ca.pem" {
			mustWrite(path("agent-ca.pem"), raw, 0644)
		}
	}
	if os.Chown(path("agent"), agentUID, agentUID) != nil || os.Chown(path("agent/invitation"), agentUID, agentUID) != nil {
		t.Fatal("cannot create private agent fixture")
	}
	var unavailable atomic.Bool
	allow := faultableApplicationProbe(t, "allowed-fixture", unavailable.Load)
	deny := localProbeEndpoint(t, "denied-fixture")
	var failAfterReplacement, failHotProbe atomic.Bool
	var crashOnReplacement atomic.Int32
	target := faultableApplicationProbe(t, "target-fixture", func() bool {
		if failHotProbe.Load() {
			return true
		}
		if !failAfterReplacement.Load() {
			return false
		}
		raw, err := exec.Command(xray, "api", "lsrules", "--server=127.0.0.1:10085", "--timeout=1").Output()
		var list struct {
			Rules []struct{ RuleTag, Tag string }
		}
		if err != nil || json.Unmarshal(raw, &list) != nil {
			return false
		}
		for _, r := range list.Rules {
			if r.RuleTag == "switch" && r.Tag == "block" {
				if !failAfterReplacement.CompareAndSwap(true, false) {
					return false
				}
				if pid := crashOnReplacement.Swap(0); pid > 0 {
					_ = syscall.Kill(int(pid), syscall.SIGKILL)
				}
				return true
			}
		}
		return false
	})
	_, deniedPort, _ := net.SplitHostPort(deny)
	_, targetPort, _ := net.SplitHostPort(target)
	free, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := free.Addr().String()
	_, proxyPort, _ := net.SplitHostPort(proxy)
	_ = free.Close()
	catalog := func(denyTarget, extra bool) *publication.VerifiedFiles {
		c := fixtureDesiredCatalog("{ \"log\": { \"loglevel\":\"none\" } }\n")
		c.Files[2].Content = []byte(fmt.Sprintf(`{"inbounds":[{"port":%s,"listen":"127.0.0.1","protocol":"socks","tag":"fixture-socks"}]}`, proxyPort))
		c.Files[3].Content = []byte(`{"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"block","protocol":"blackhole"}]}`)
		ports := deniedPort
		if denyTarget {
			ports += "," + targetPort
		}
		c.Files[4].Content = []byte(fmt.Sprintf(`{"routing":{"rules":[{"type":"field","port":%q,"outboundTag":"block"}]}}`, ports))
		if mode == "hot" {
			c.Files[0].Content = []byte(`{"log":{"loglevel":"warning"}}`)
			c.Files[1].Content = []byte(`{"api":{"tag":"api","listen":"127.0.0.1:10085","services":["RoutingService","StatsService"]},"stats":{}}`)
			c.Files[2].Content = []byte(fmt.Sprintf(`{"inbounds":[{"port":%s,"listen":"127.0.0.1","protocol":"socks","tag":"fixture-socks","settings":{"auth":"noauth","udp":false}}]}`, proxyPort))
			out := "direct"
			if denyTarget {
				out = "block"
			}
			c.Files[4].Content = []byte(fmt.Sprintf(`{"routing":{"domainStrategy":"AsIs","rules":[{"type":"field","ruleTag":"health","inboundTag":["fixture-socks"],"port":%q,"outboundTag":"block"},{"type":"field","ruleTag":"switch","inboundTag":["fixture-socks"],"port":%q,"outboundTag":%q}]}}`, deniedPort, targetPort, out))
			return c
		}
		if extra {
			c.Files = append(c.Files, configuration.CatalogFile{Name: "06_extra.json", Content: []byte("{ \"stats\": {} }\n")})
		}
		return c
	}
	initial := catalog(false, false)
	for _, file := range initial.Files {
		mustWrite(path("work/"+file.Name), file.Content, 0644)
	}
	mustWrite(path("work/notes.txt"), []byte("preserved-fixture"), 0644)
	working := startProcess(t, xray, "run", "-confdir", path("work"))
	waitSocket(t, "tcp", proxy)
	oldPID := working.cmd.Process.Pid
	oldTick, err := localvalidation.ProcessStartTick(oldPID)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.ReadFile(xray)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(executable)
	manifest, err := localvalidation.WorkingManifest(path("work"))
	if err != nil {
		t.Fatal(err)
	}
	scope := preparation.Scope{Origin: f.origin, RouterID: recoveryRouter, Profile: configuration.XrayIdentityVersion, VerifierID: "entware-direct-application"}
	cfg := localvalidation.Config{Version: 1, Scope: scope, Socket: path("socket/validator.sock"),
		StageDirectory: path("stage"), CgroupDirectory: cgroup, AgentUID: agentUID, AgentGID: agentUID,
		WorkerUID: workerUID, WorkerGID: workerUID, Xray: xray, XraySHA256: hex.EncodeToString(digest[:]),
		WorkingDirectory: path("work"), WorkingManifest: manifest, WorkingPID: oldPID, WorkingStartTick: oldTick,
		SOCKS: proxy, AllowURL: "http://" + allow, AllowMarker: "allowed-fixture", DenyURL: "http://" + deny, DenyMarker: "denied-fixture"}
	if mode == "hot" {
		filter, err := os.ReadFile("/opt/sbin/iptables")
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(filter)
		cfg.Hot = &localvalidation.HotConfig{API: "127.0.0.1:10085", ProbeURL: "http://" + target, ProbeMarker: "target-fixture", FirewallSHA256: hex.EncodeToString(sum[:])}
	}
	app := localvalidation.ApplicationConfig{Version: 1, Mode: "direct-xray-lab", Socket: path("socket/apply.sock"),
		JournalDirectory: path("journal"), CA: path("trusted-ca.pem"), RuntimeUID: runtimeUID, RuntimeGID: runtimeUID}
	writeConfig := func(name string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(path(name), raw, 0600)
	}
	writeConfig("profile.json", cfg)
	writeConfig("application.json", app)
	startSupervisor := func() *process {
		t.Helper()
		p := startProcess(t, binary, "--config", path("profile.json"), "--application-config", path("application.json"))
		waitRecovery(t, 12*time.Second, "controlled supervisor did not start", func() bool {
			select {
			case <-p.done:
				t.Fatalf("controlled supervisor failed: %s", p.output())
			default:
				return strings.Contains(p.output(), "local application ready")
			}
			return false
		})
		return p
	}
	supervisor := startSupervisor()
	publish := func(files *publication.VerifiedFiles, number int64) {
		t.Helper()
		before, err := f.store.Read(t.Context(), publication.ReadRequest{RouterID: scope.RouterID})
		if err != nil {
			t.Fatal(err)
		}
		got, err := f.store.Commit(t.Context(), publication.CommitPlan{
			Expected: publication.Expectations{RouterID: scope.RouterID, TemplateID: before.TemplateID,
				AcceptedVersion: before.Accepted.Version, ActiveGeneration: before.Active.Generation, DesiredNumber: before.Desired.Number},
			Settings: before.Active.Draft, NewRevision: files,
		})
		if err != nil || got != number {
			t.Fatalf("publish revision %d: %v", got, err)
		}
	}
	launchAgent := func() (func(), *safeBuffer) {
		t.Helper()
		output := new(safeBuffer)
		cmd := exec.CommandContext(t.Context(), f.agent, "--server", f.origin, "--state-dir", path("agent"),
			"--ca-file", path("agent-ca.pem"), "--local-application-socket", app.Socket, "--local-verifier", scope.VerifierID)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: agentUID, Gid: agentUID}}
		cmd.Stdout, cmd.Stderr = output, output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		stop := func() {
			if cmd.Process.Signal(syscall.SIGTERM) == nil {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					_ = cmd.Process.Kill()
				}
			}
		}
		t.Cleanup(stop)
		waitRecovery(t, 45*time.Second, "agent did not complete application", func() bool {
			select {
			case err := <-done:
				t.Fatalf("agent stopped early: %v, %s", err, output.String())
			default:
				return strings.Contains(output.String(), "agent authenticated")
			}
			return false
		})
		return stop, output
	}
	committed := func() (restart.Confirmed, []byte) {
		t.Helper()
		raw, err := os.ReadFile(path("journal/application.json"))
		var state struct{ Current *restart.Confirmed }
		if err != nil || json.Unmarshal(raw, &state) != nil || state.Current == nil {
			t.Fatal("no durable application result")
		}
		return *state.Current, raw
	}
	checkTraffic := func(address, marker string, allowed bool) {
		t.Helper()
		transport := &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "socks5", Host: proxy}), DisableKeepAlives: true}
		client := &http.Client{Transport: transport, Timeout: time.Second}
		defer transport.CloseIdleConnections()
		response, err := client.Get("http://" + address)
		if response != nil {
			defer response.Body.Close()
		}
		if allowed {
			if err != nil || response.StatusCode != 200 {
				t.Fatal("expected current Xray to allow synthetic request")
			}
			got, err := io.ReadAll(io.LimitReader(response.Body, int64(len(marker)+1)))
			if err != nil || string(got) != marker {
				t.Fatal("unexpected loopback response")
			}
		} else if err == nil {
			t.Fatal("expected current Xray to deny synthetic request")
		}
	}
	checkTraffic(target, "target-fixture", true)
	second := catalog(true, true)
	publish(second, 2)
	stop, output := launchAgent()
	if !strings.Contains(output.String(), "agent revision verified locally") {
		t.Fatalf("first application did not succeed: %s", output.String())
	}
	first, _ := committed()
	if first.Number != 2 || first.Baseline.Runtime.PID == oldPID || first.Baseline.Runtime.BootID == "" {
		t.Fatal("first restart did not commit a new runtime")
	}
	if mode == "hot" {
		if first.Guard == "" {
			t.Fatal("protected launcher did not prove guarded startup")
		}
		assertHotAPIGuard(t, xray, cfg.Hot.API, proxy, agentUID)
		stop()
		checkTraffic(target, "target-fixture", false)
		candidate := catalog(false, false)
		publish(candidate, 3)
		stop2, out := launchAgent()
		stop2()
		next, _ := committed()
		if next.Number != 3 || next.Hot == nil || next.Hot.Outcome != "direct" || next.Baseline.Runtime != first.Baseline.Runtime {
			t.Fatalf("hot application failed number=%d hot=%t log=%s", next.Number, next.Hot != nil, out.String())
		}
		checkTraffic(target, "target-fixture", true)
		checkTraffic(allow, "allowed-fixture", true)
		checkTraffic(deny, "denied-fixture", false)
		for _, file := range candidate.Files {
			raw, err := os.ReadFile(path("work/" + file.Name))
			if err != nil || string(raw) != string(file.Content) {
				t.Fatal("target file mismatch")
			}
		}
		stopRouterAgent(t, supervisor)
		supervisor = startSupervisor()
		stop3, out := launchAgent()
		stop3()
		replay, _ := committed()
		if replay.AttemptID != next.AttemptID || replay.Baseline.Runtime != next.Baseline.Runtime || !strings.Contains(out.String(), "agent revision verified locally") {
			t.Fatal("reopened hot receipt did not verify")
		}
		stopRouterAgent(t, supervisor)
		if err := syscall.Kill(next.Baseline.Runtime.PID, syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		waitRecovery(t, 6*time.Second, "hot process did not terminate", func() bool { _, err := localvalidation.ProcessStartTick(next.Baseline.Runtime.PID); return err != nil })
		// Model a protected receipt from an earlier boot/network namespace while
		// retaining the same stable profile and exact committed bytes.
		data, err := os.ReadFile(path("journal/application.json"))
		if err != nil {
			t.Fatal(err)
		}
		var oldEpoch restart.Record
		if json.Unmarshal(data, &oldEpoch) != nil || oldEpoch.Current == nil || oldEpoch.Current.Hot == nil {
			t.Fatal("missing old epoch")
		}
		stable, _, ok := strings.Cut(oldEpoch.Current.Guard, ":")
		if !ok {
			t.Fatal("receipt mixes stable profile and network incarnation")
		}
		oldEpoch.Current.Guard = stable + ":" + strings.Repeat("0", 64)
		oldEpoch.Current.Hot.Guard = oldEpoch.Current.Guard
		oldEpoch.Current.Baseline.Runtime.BootID = "11111111-1111-4111-8111-111111111111"
		writeConfig("journal/application.json", oldEpoch)
		failHotProbe.Store(true)
		bad := startProcess(t, binary, "--config", path("profile.json"), "--application-config", path("application.json"))
		if err := bad.wait(t); err == nil {
			t.Fatal("cold launcher accepted missing third probe")
		}
		if conn, err := net.DialTimeout("tcp", proxy, time.Second); err == nil {
			conn.Close()
			t.Fatal("unrecorded working process survived failed proof")
		}
		failHotProbe.Store(false)
		supervisor = startSupervisor()
		revived, _ := committed()
		if revived.Hot == nil || revived.AttemptID != next.AttemptID || revived.Baseline.Runtime == next.Baseline.Runtime {
			t.Fatal("hot generation cold restart proof")
		}
		checkTraffic(target, "target-fixture", true)
		checkTraffic(deny, "denied-fixture", false)
		failAfterReplacement.Store(true)
		publish(catalog(true, false), 4)
		failed, output := launchAgent()
		failed()
		var record restart.Record
		raw, err := os.ReadFile(path("journal/application.json"))
		if err != nil || json.Unmarshal(raw, &record) != nil || failAfterReplacement.Load() || record.LastRollback == nil || record.LastRollback.FailedPhase != restart.HotDispatch || record.Current == nil || record.Current.AttemptID != revived.AttemptID || record.Current.Baseline.Runtime == revived.Baseline.Runtime || record.Pending != nil || !strings.Contains(output.String(), "previous runtime restored") {
			t.Fatalf("hot post-command rollback failed, invoked=%t log=%s", !failAfterReplacement.Load(), output.String())
		}
		checkTraffic(target, "target-fixture", true)
		for _, file := range candidate.Files {
			raw, err := os.ReadFile(path("work/" + file.Name))
			if err != nil || !bytes.Equal(raw, file.Content) {
				t.Fatal("hot rollback lost previous bytes")
			}
		}
		// Interrupt the actual supervisor after Xray exposes the new route and
		// before it witnesses the command result. No synthetic journal is injected.
		crashOnReplacement.Store(int32(supervisor.cmd.Process.Pid))
		failAfterReplacement.Store(true)
		publish(catalog(true, false), 5)
		interrupted, _ := launchAgent()
		interrupted()
		if supervisor.wait(t) == nil || failAfterReplacement.Load() {
			t.Fatal("supervisor was not interrupted during hot dispatch")
		}
		raw, err = os.ReadFile(path("journal/application.json"))
		record = restart.Record{}
		if err != nil || json.Unmarshal(raw, &record) != nil || record.Pending == nil || record.Pending.Phase != restart.HotDispatch || record.Pending.TargetFiles == nil {
			t.Fatal("interrupted hot intent lost")
		}
		attempt := record.Pending.AttemptID
		checkTraffic(target, "target-fixture", false)
		supervisor = startSupervisor()
		raw, err = os.ReadFile(path("journal/application.json"))
		record = restart.Record{}
		if err != nil || json.Unmarshal(raw, &record) != nil || record.Pending != nil || record.LastRollback == nil || record.LastRollback.AttemptID != attempt || record.Current == nil || record.Current.AttemptID != revived.AttemptID {
			t.Fatal("relaunch failed to resolve hot intent")
		}
		checkTraffic(target, "target-fixture", true)
		stopRouterAgent(t, supervisor)
		return
	}
	extra, err := os.Stat(path("work/06_extra.json"))
	if err != nil || extra.Mode().Perm() != 0640 || extra.Sys().(*syscall.Stat_t).Gid != runtimeUID {
		t.Fatal("unexpected extra file metadata")
	}
	checkTraffic(target, "target-fixture", false)
	checkTraffic(allow, "allowed-fixture", true)
	if mode == "startup-recovery" {
		stop()
		runStartupRecoveryFixture(t, binary, path, scope, runtimeUID, supervisor, startSupervisor, first, catalog(false, false), func() {
			checkTraffic(allow, "allowed-fixture", true)
			checkTraffic(target, "target-fixture", false)
		})
		if status := f.status(t); status.Applied != 0 || status.Last != nil {
			t.Fatal("startup recovery fabricated a server report")
		}
		return
	}
	if mode != "apply" {
		stop()
		readRollback := func() (restart.Record, []byte) {
			t.Helper()
			raw, err := os.ReadFile(path("journal/application.json"))
			var record restart.Record
			if err != nil || json.Unmarshal(raw, &record) != nil {
				t.Fatalf("cannot read protected rollback diagnostics: %v", err)
			}
			return record, raw
		}
		if mode == "conflict" {
			_, allowPort, _ := net.SplitHostPort(allow)
			target := catalog(false, false)
			target.Files[4].Content = []byte(fmt.Sprintf(`{"routing":{"rules":[{"type":"field","port":%q,"outboundTag":"block"}]}}`, deniedPort+","+allowPort))
			publish(target, 3)
			foreignPath := path("work/extra.toml")
			foreign := []byte("# external fixture\n")
			injected := make(chan error, 1)
			go func() {
				for until := time.Now().Add(8 * time.Second); time.Now().Before(until); {
					raw, err := os.ReadFile(path("work/05_routing.json"))
					if err == nil && bytes.Equal(raw, target.Files[4].Content) {
						injected <- os.WriteFile(foreignPath, foreign, 0644)
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				injected <- fmt.Errorf("candidate disk transition was not observed")
			}()
			stopConflict, output := launchAgent()
			stopConflict()
			if err := <-injected; err != nil {
				t.Fatal(err)
			}
			record, _ := readRollback()
			if !strings.Contains(output.String(), "rollback failed; current runtime unknown") ||
				record.Pending == nil || record.Pending.Rollback == nil || record.Pending.Rollback.State != restart.RollbackFailed ||
				record.Current == nil || record.Current.AttemptID != first.AttemptID {
				t.Fatal("foreign content did not block rollback")
			}
			if raw, err := os.ReadFile(foreignPath); err != nil || !bytes.Equal(raw, foreign) {
				t.Fatal("rollback modified foreign loaded file")
			}
			stopRouterAgent(t, supervisor)
			return
		}
		// Xray's configuration test succeeds, but startup cannot bind this
		// separate fixture HTTP listener. The target drops the optional file.
		invalidStart := catalog(false, false)
		_, allowPort, _ := net.SplitHostPort(allow)
		invalidStart.Files[2].Content = bytes.ReplaceAll(invalidStart.Files[2].Content, []byte(proxyPort), []byte(allowPort))
		publish(invalidStart, 3)
		stopFailed, failedOutput := launchAgent()
		stopFailed()
		recovered, raw := readRollback()
		if !strings.Contains(failedOutput.String(), "previous runtime restored") || recovered.Pending != nil ||
			recovered.LastRollback == nil || recovered.LastRollback.TargetNumber != 3 || recovered.Current == nil ||
			recovered.Current.Number != 2 || recovered.Current.AttemptID != first.AttemptID ||
			recovered.Current.Baseline.Runtime == first.Baseline.Runtime ||
			!reflect.DeepEqual(recovered.Current.Baseline.Files, first.Baseline.Files) {
			t.Fatalf("failed start did not restore expected previous baseline: %s", failedOutput.String())
		}
		restored, err := os.Stat(path("work/06_extra.json"))
		if err != nil || restored.Mode().Perm() != 0640 || restored.Sys().(*syscall.Stat_t).Gid != runtimeUID {
			t.Fatal("previous optional file did not recover its metadata")
		}
		checkTraffic(allow, "allowed-fixture", true)
		checkTraffic(target, "target-fixture", false)
		stopReplay, _ := launchAgent()
		stopReplay()
		if _, again := readRollback(); !bytes.Equal(again, raw) {
			t.Fatal("automatic cycle retried identical failed revision")
		}

		// Provision one retry via the same protected application supervisor.
		id := recovered.LastRollback.AttemptID
		stopRouterAgent(t, supervisor)
		if out := commandLine(t, binary, "--config", path("profile.json"), "--application-config", path("application.json"), "--retry-rollback", id); out != "one local retry authorized" {
			t.Fatal("local retry permit was not recorded")
		}
		supervisor = startSupervisor()
		stopOnce, onceOutput := launchAgent()
		stopOnce()
		once, onceRaw := readRollback()
		if !strings.Contains(onceOutput.String(), "previous runtime restored") || once.LastRollback == nil ||
			once.LastRollback.AttemptID == id || once.LastRollback.RetryApproved || once.Current.Baseline.Runtime == recovered.Current.Baseline.Runtime {
			t.Fatal("explicit retry did not execute exactly once")
		}
		checkTraffic(allow, "allowed-fixture", true)
		stopRepeat, _ := launchAgent()
		stopRepeat()
		if _, again := readRollback(); !bytes.Equal(again, onceRaw) {
			t.Fatal("one-shot retry was reused")
		}

		// A new desired revision is allowed. After it reaches the live disk,
		// disable the independent allowed target so neither new runtime nor
		// restored runtime can furnish proof. Rollback must keep uncertainty.
		noControl := catalog(false, false)
		noControl.Files[4].Content = []byte(fmt.Sprintf(`{"routing":{"rules":[{"type":"field","port":%q,"outboundTag":"block"}]}}`, deniedPort+","+allowPort))
		publish(noControl, 4)
		armed := make(chan bool, 1)
		go func() {
			for until := time.Now().Add(8 * time.Second); time.Now().Before(until); {
				raw, err := os.ReadFile(path("work/05_routing.json"))
				if err == nil && bytes.Equal(raw, noControl.Files[4].Content) {
					unavailable.Store(true)
					armed <- true
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			armed <- false
		}()
		stopUnknown, unknownOutput := launchAgent()
		stopUnknown()
		unknown, unknownRaw := readRollback()
		var wire struct{ Pending struct{ Number int64 } }
		if json.Unmarshal(unknownRaw, &wire) != nil {
			t.Fatal("cannot read protected failed target number")
		}
		if !<-armed || !strings.Contains(unknownOutput.String(), "rollback failed; current runtime unknown") ||
			unknown.Current == nil || !reflect.DeepEqual(unknown.Current, once.Current) || unknown.Pending == nil ||
			wire.Pending.Number != 4 || unknown.Pending.Rollback == nil || unknown.Pending.Rollback.State != restart.RollbackFailed {
			t.Fatalf("rollback failure did not preserve the historical result and barrier: %s", unknownOutput.String())
		}
		stopBlocked, _ := launchAgent()
		stopBlocked()
		if _, again := readRollback(); !bytes.Equal(again, unknownRaw) {
			t.Fatal("terminal rollback failure was retried")
		}
		stopRouterAgent(t, supervisor)
		blocked := startProcess(t, binary, "--config", path("profile.json"), "--application-config", path("application.json"))
		if err := blocked.wait(t); err == nil || !strings.Contains(blocked.output(), "rollback failed; current runtime unknown") {
			t.Fatal("ordinary start bypassed retained rollback barrier")
		}
		if conn, err := net.DialTimeout("tcp", proxy, time.Second); err == nil {
			conn.Close()
			t.Fatal("unverified listener remained after rollback failure")
		}
		status := f.status(t)
		if status.Applied != 0 || status.Last != nil {
			t.Fatal("rollback was sent as an applied result to the server")
		}
		return
	}
	third := catalog(false, false)
	publish(third, 3)
	waitRecovery(t, 20*time.Second, "second publication did not use active agent watch", func() bool {
		return strings.Count(output.String(), "agent revision verified locally") == 2
	})
	stop()
	current, raw := committed()
	if current.Number != 3 || current.Baseline.Runtime == first.Baseline.Runtime ||
		current.AttemptID == first.AttemptID || len(current.Baseline.Files) != len(third.Files) {
		t.Fatal("second restart did not use successor baseline")
	}
	for i, file := range third.Files {
		data, err := os.ReadFile(path("work/" + file.Name))
		if err != nil || !bytes.Equal(file.Content, data) || !bytes.Equal(file.Content, current.Baseline.Files[i].Content) {
			t.Fatal("committed or live configuration bytes changed")
		}
	}
	if _, err := os.Stat(path("work/06_extra.json")); !os.IsNotExist(err) {
		t.Fatal("obsolete optional file survived")
	}
	if notes, _ := os.ReadFile(path("work/notes.txt")); string(notes) != "preserved-fixture" {
		t.Fatal("auxiliary file changed")
	}
	checkTraffic(target, "target-fixture", true)
	// A structurally valid but agent-owned receipt is outside the supervisor's trust boundary.
	var forged restart.Record
	if err := json.Unmarshal(raw, &forged); err != nil || forged.Current == nil {
		t.Fatal("cannot make synthetic receipt")
	}
	forged.Current.Baseline.Runtime.PID = oldPID
	writeConfig("agent/application.json", forged)
	if err := os.Chown(path("agent/application.json"), agentUID, agentUID); err != nil {
		t.Fatal(err)
	}
	replay, replayOutput := launchAgent()
	replay()
	if !strings.Contains(replayOutput.String(), "agent revision verified locally") {
		t.Fatalf("current revision replay failed: %s", replayOutput.String())
	}
	if again, saved := committed(); !reflect.DeepEqual(again, current) || !bytes.Equal(saved, raw) {
		t.Fatal("replay rewrote the transaction or restarted the generation")
	}

	// Simulate a clean service stop, then the ordinary launcher start path.
	stopRouterAgent(t, supervisor)
	process, err := os.FindProcess(current.Baseline.Runtime.PID)
	if err != nil || process.Signal(syscall.SIGTERM) != nil {
		t.Fatal("could not stop committed synthetic Xray")
	}
	waitRecovery(t, 6*time.Second, "committed process still live", func() bool {
		_, err := localvalidation.ProcessStartTick(current.Baseline.Runtime.PID)
		return err != nil
	})
	// A byte-identical directory copied into the same path cannot replace the
	// committed directory identity.
	if err := os.Rename(path("work"), path("original-work")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path("work"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, file := range third.Files {
		mustWrite(path("work/"+file.Name), file.Content, 0644)
	}
	foreign := startProcess(t, binary, "--config", path("profile.json"), "--application-config", path("application.json"))
	if err := foreign.wait(t); err == nil || strings.Contains(foreign.output(), "local application ready") {
		t.Fatal("launcher accepted a directory with a different inode")
	}
	if err := os.Rename(path("work"), path("foreign-work")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path("original-work"), path("work")); err != nil {
		t.Fatal(err)
	}
	restarted := startSupervisor()
	final, _ := committed()
	if final.AttemptID != current.AttemptID || final.Number != 3 || final.Baseline.Runtime == current.Baseline.Runtime {
		t.Fatal("trusted ordinary startup did not attest the committed generation")
	}
	checkTraffic(target, "target-fixture", true)
	status := f.status(t)
	if status.Applied != 0 || status.Last != nil {
		t.Fatal("application leaked into server reports")
	}
	stopRouterAgent(t, restarted)
	journal, err := restart.OpenJournal(app.JournalDirectory, scope)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := journal.Load()
	if err != nil || saved.Current == nil || !reflect.DeepEqual(*saved.Current, final) {
		t.Fatal("independent startup could not read the previous result", err)
	}
	nextPlan := catalog(true, true)
	next := agentrevision.Snapshot{Number: 4, Profile: nextPlan.Profile, Files: nextPlan.Files}
	tag, err := agentrevision.Tag(scope.RouterID, next)
	if err != nil {
		t.Fatal(err)
	}
	targetFiles, err := restart.PlanWorkingGeneration(scope, final.Baseline, next, runtimeUID)
	if err != nil {
		t.Fatal(err)
	}
	saved.Pending = &restart.Intent{AttemptID: "01010101010101010101010101010101", Previous: final.Baseline,
		Target: restart.Desired{Tag: tag, Snapshot: next}, TargetFiles: targetFiles, Phase: restart.Stopped}
	if err := journal.Save(saved); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	child, err := os.FindProcess(final.Baseline.Runtime.PID)
	if err != nil || child.Signal(syscall.SIGTERM) != nil {
		t.Fatal("could not stop committed synthetic Xray")
	}
	waitRecovery(t, 6*time.Second, "committed process still live", func() bool {
		_, err := localvalidation.ProcessStartTick(final.Baseline.Runtime.PID)
		return err != nil
	})
	// Simulate an interrupted partial file replacement before restarting the owner.
	changed := next.Files[4]
	mustWrite(path("work/"+changed.Name), changed.Content, 0644)
	recovered := startSupervisor()
	defer stopRouterAgent(t, recovered)
	proof, _ := committed()
	if proof.AttemptID != final.AttemptID || proof.Number != final.Number ||
		!reflect.DeepEqual(proof.Baseline.Files, final.Baseline.Files) {
		t.Fatal("startup exposed the mixed generation")
	}
	checkTraffic(target, "target-fixture", true)
}

func faultableApplicationProbe(t *testing.T, marker string, unavailable func() bool) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if unavailable() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, marker)
	}), ReadHeaderTimeout: time.Second}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return listener.Addr().String()
}

// The filter's packet counter distinguishes an actual rejected connection
// from an unrelated parser, SOCKS handshake or client binary failure.
func assertHotAPIGuard(t *testing.T, xray, api, socks string, uid uint32) {
	t.Helper()
	count := func() uint64 {
		b, err := exec.Command("/opt/sbin/iptables", "-nvxL", "OUTPUT").Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "REJECT") && strings.Contains(line, "dpt:10085") {
				fields := strings.Fields(line)
				c, err := strconv.ParseUint(fields[0], 10, 64)
				if err == nil {
					return c
				}
			}
		}
		t.Fatal("missing exact synthetic API counter")
		return 0
	}
	before := count()
	cmd := exec.Command(xray, "api", "lsrules", "--server="+api, "--timeout=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: uid}}
	if err := cmd.Run(); err == nil || count() <= before {
		t.Fatal("foreign UID was not rejected at API endpoint")
	}
	before = count()
	client := http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "socks5", Host: socks}), DisableKeepAlives: true}, Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + api)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil || count() <= before {
		t.Fatal("proxied Xray UID was not rejected at API endpoint")
	}
}
