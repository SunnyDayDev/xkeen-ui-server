//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/localvalidation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func TestProcessAgentPreparesOnlyVerifiedLocalCandidate(t *testing.T) {
	validator := os.Getenv("XKEEN_TEST_PREPARATION_BINARY")
	xray := os.Getenv("XKEEN_TEST_XRAY")
	cgroup := os.Getenv("XKEEN_TEST_CGROUP")
	if validator == "" || xray == "" || cgroup == "" || os.Geteuid() != 0 {
		t.Skip("requires disposable delegated Entware profile")
	}
	ctx := t.Context()
	agent := buildAgentForProcessTest(t)
	const agentUID, workerUID = 65532, 65534
	dbPath := filepath.Join(t.TempDir(), "server.sqlite")
	invitation, _ := fixtureEnrollment(t, dbPath)
	certPath, keyPath, roots := fixtureServerCertificate(t)
	srv := startProcess(t, testBinary, "--db-path", dbPath, "--listen", "127.0.0.1:0", "--tls-cert", certPath, "--tls-key", keyPath)
	web := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	defer web.CloseIdleConnections()
	origin := "https://" + srv.readyWithClient(t, web, "https")

	root, err := os.MkdirTemp("", "e02-process-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	path := func(name string) string { return filepath.Join(root, name) }
	for _, name := range []string{"work", "agent", "stage", "socket"} {
		if err := os.Mkdir(path(name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(path("socket"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path("agent"), agentUID, agentUID); err != nil {
		t.Fatal(err)
	}
	write := func(file string, b []byte, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(file, b, mode); err != nil {
			t.Fatal(err)
		}
	}
	write(path("agent/invitation"), []byte(invitation), 0600)
	if err := os.Chown(path("agent/invitation"), agentUID, agentUID); err != nil {
		t.Fatal(err)
	}
	ca, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	write(path("ca.pem"), ca, 0644)

	workDir := path("work")
	useXKeen := os.Getenv("XKEEN_TEST_PREPARATION_XKEEN") == "1"
	if useXKeen {
		workDir = "/opt/etc/xray/configs"
	}
	allowed, denied := localProbeEndpoint(t, "allowed-fixture"), localProbeEndpoint(t, "denied-fixture")
	_, denyPort, _ := net.SplitHostPort(denied)
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyAddress := free.Addr().String()
	_, proxyPort, _ := net.SplitHostPort(proxyAddress)
	free.Close()
	live := fixtureDesiredCatalog(`{"log":{"loglevel":"none"}}`)
	live.Files[2].Content = []byte(fmt.Sprintf(`{"inbounds":[{"port":%s,"listen":"127.0.0.1","protocol":"socks","tag":"fixture-socks"}]}`, proxyPort))
	live.Files[3].Content = []byte(`{"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"block","protocol":"blackhole"}]}`)
	live.Files[4].Content = []byte(fmt.Sprintf(`{"routing":{"rules":[{"type":"field","port":"%s","outboundTag":"block"}]}}`, denyPort))
	for _, file := range live.Files {
		write(filepath.Join(workDir, file.Name), file.Content, 0644)
	}
	write(filepath.Join(workDir, "notes.txt"), []byte("preserved-fixture"), 0644)
	manifest := commandLine(t, validator, "--working-manifest", workDir)
	var liveProcess *process
	startWorking := func() int {
		if useXKeen {
			quietCommand(t, "/opt/sbin/xkeen", "-start")
			pid, err := strconv.Atoi(commandLine(t, "/usr/bin/pgrep", "-x", "xray"))
			if err != nil {
				t.Fatal("lab XKeen started no unique Xray")
			}
			waitSocket(t, "tcp", proxyAddress)
			return pid
		}
		liveProcess = startProcess(t, xray, "run", "-confdir", workDir)
		waitSocket(t, "tcp", proxyAddress)
		return liveProcess.cmd.Process.Pid
	}
	stopWorking := func() {
		if useXKeen {
			quietCommand(t, "/opt/sbin/xkeen", "-stop")
			return
		}
		stopRouterAgent(t, liveProcess)
	}
	pid := startWorking()
	t.Cleanup(func() {
		if useXKeen {
			_ = exec.Command("/opt/sbin/xkeen", "-stop").Run()
		}
	})
	startTick := func(pid int) uint64 {
		tickText := commandLine(t, validator, "--working-process", strconv.Itoa(pid))
		tick, err := strconv.ParseUint(tickText, 10, 64)
		if err != nil || tick == 0 {
			t.Fatal("unavailable working process receipt")
		}
		return tick
	}
	tick := startTick(pid)
	binary, err := os.ReadFile(xray)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	scope := preparation.Scope{Origin: origin, RouterID: "router-example", Profile: configuration.XrayIdentityVersion, VerifierID: "entware-lab-v1"}
	cfg := localvalidation.Config{Version: 1, Scope: scope, Socket: path("socket/validator.sock"),
		StageDirectory: path("stage"), CgroupDirectory: cgroup, AgentUID: agentUID, AgentGID: agentUID,
		WorkerUID: workerUID, WorkerGID: workerUID, Xray: xray, XraySHA256: hex.EncodeToString(digest[:]),
		WorkingDirectory: workDir, WorkingManifest: manifest, WorkingPID: pid, WorkingStartTick: tick,
		SOCKS: proxyAddress, AllowURL: "http://" + allowed, AllowMarker: "allowed-fixture", DenyURL: "http://" + denied, DenyMarker: "denied-fixture"}
	startSupervisor := func() *process {
		profile, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		write(path("profile.json"), profile, 0600)
		p := startProcess(t, validator, "--config", path("profile.json"))
		for end := time.Now().Add(5 * time.Second); ; {
			select {
			case <-p.done:
				t.Fatalf("supervisor failed to create protected socket: %s", p.output())
			default:
			}
			if strings.Contains(p.output(), "local validator ready") {
				break
			}
			if time.Now().After(end) {
				t.Fatal("supervisor startup timed out")
			}
			time.Sleep(10 * time.Millisecond)
		}
		return p
	}
	supervisor := startSupervisor()

	launch := func() string {
		t.Helper()
		var output safeBuffer
		cmd := exec.CommandContext(ctx, agent, "--server", origin, "--state-dir", path("agent"), "--ca-file", path("ca.pem"),
			"--local-validator-socket", cfg.Socket, "--local-verifier", scope.VerifierID)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: agentUID, Gid: agentUID, NoSetGroups: false}}
		cmd.Stdout = &output
		cmd.Stderr = &output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		defer cmd.Process.Kill()
		for end := time.Now().Add(15 * time.Second); ; {
			select {
			case err := <-done:
				t.Fatalf("agent stopped early: %v, %s", err, output.String())
			default:
			}
			if strings.Contains(output.String(), "agent authenticated") {
				break
			}
			if time.Now().After(end) {
				t.Fatalf("agent did not finish its initial preparation: %s", output.String())
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("agent did not stop gracefully")
		}
		return output.String()
	}
	first := launch()
	if !strings.Contains(first, "agent revision prepared") {
		t.Fatalf("agent did not prepare current revision: %s", first)
	}
	preparedFile := path("agent/prepared.json")
	saved, err := os.ReadFile(preparedFile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(saved, []byte(invitation)) {
		t.Fatal("invitation reached prepared package")
	}
	info, err := os.Stat(preparedFile)
	if err != nil || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Uid != agentUID {
		t.Fatal("unprivileged agent did not own a private package")
	}

	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := sqlite.NewPublications(db)
	publish := func(catalog *publication.VerifiedFiles) {
		before, err := store.Read(ctx, publication.ReadRequest{RouterID: scope.RouterID})
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.Commit(ctx, publication.CommitPlan{
			Expected: publication.Expectations{RouterID: scope.RouterID, TemplateID: before.TemplateID, AcceptedVersion: before.Accepted.Version,
				ActiveGeneration: before.Active.Generation, DesiredNumber: before.Desired.Number},
			Settings: before.Active.Draft, NewRevision: catalog})
		if err != nil {
			t.Fatal(err)
		}
	}
	bad := fixtureDesiredCatalog("{}")
	bad.Files[3].Content = []byte(`{"outbounds":[{"protocol":"invalid-demo-protocol"}]}`)
	publish(bad)
	negative := launch()
	if strings.Contains(negative, "invalid-demo-protocol") || !strings.Contains(negative, "agent candidate configuration invalid") {
		t.Fatalf("unsafe or unexpected rejection: %s", negative)
	}
	still, err := os.ReadFile(preparedFile)
	if err != nil || !bytes.Equal(saved, still) {
		t.Fatal("invalid candidate changed prior preparation")
	}
	if tickNow, err := localvalidation.ProcessStartTick(cfg.WorkingPID); err != nil || tickNow != tick {
		t.Fatal("local validation changed live process")
	}
	if after, err := localvalidation.WorkingManifest(workDir); err != nil || after != manifest {
		t.Fatal("local validation changed live files")
	}
	if _, err := cfg.Observer().Observe(ctx); err != nil {
		t.Fatalf("control traffic changed: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(workDir, "notes.txt")); err != nil || string(data) != "preserved-fixture" {
		t.Fatal("non-loaded file was modified")
	}
	// The same files under a fresh process require a new trusted launch proof.
	publish(fixtureDesiredCatalog("{ }\n"))
	stopWorking()
	cfg.WorkingPID = startWorking()
	if cfg.WorkingPID == pid {
		t.Fatal("fixture did not start a fresh process")
	}
	refused := launch()
	if !strings.Contains(refused, "agent working baseline unavailable") {
		t.Fatalf("stale live proof was accepted: %s", refused)
	}
	if current, err := os.ReadFile(preparedFile); err != nil || !bytes.Equal(saved, current) {
		t.Fatal("a stale proof changed prior preparation")
	}
	if err := supervisor.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = supervisor.wait(t)
	cfg.WorkingStartTick = startTick(cfg.WorkingPID)
	supervisor = startSupervisor()
	if resumed := launch(); !strings.Contains(resumed, "agent revision prepared") {
		t.Fatalf("fresh trusted proof did not restore preparation: %s", resumed)
	}
	var oldRecord, newRecord struct {
		BaselineID string
		Baseline   preparation.Baseline
		Revision   int64
	}
	raw, err := os.ReadFile(preparedFile)
	if err != nil || json.Unmarshal(saved, &oldRecord) != nil || json.Unmarshal(raw, &newRecord) != nil ||
		oldRecord.BaselineID != newRecord.BaselineID || oldRecord.Baseline.Runtime.PID == newRecord.Baseline.Runtime.PID ||
		newRecord.Revision != 3 {
		t.Fatal("resumed preparation changed baseline history or did not record new process")
	}
	stopRouterAgent(t, supervisor)
	stopWorking()
}

func localProbeEndpoint(t *testing.T, marker string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, marker) }),
		ReadHeaderTimeout: time.Second}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return listener.Addr().String()
}

func waitSocket(t *testing.T, network, address string) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); ; {
		conn, err := net.DialTimeout(network, address, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		if time.Now().After(end) {
			t.Fatal("working service did not open its test port")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func commandLine(t *testing.T, binary string, args ...string) string {
	t.Helper()
	c := exec.CommandContext(t.Context(), binary, args...)
	out, err := c.Output()
	if err != nil {
		t.Fatalf("trusted operator evidence could not be collected: %v", err)
	}
	return strings.TrimSpace(string(out))
}

type safeBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buffer.Write(p)
}
func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buffer.String()
}

func quietCommand(t *testing.T, binary string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, binary, args...).Run(); err != nil {
		t.Fatalf("trusted laboratory service command failed: %v", err)
	}
}
