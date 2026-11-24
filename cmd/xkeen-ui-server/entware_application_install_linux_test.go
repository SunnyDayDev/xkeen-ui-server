package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/localvalidation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestEntwareInstallManagedApplication(t *testing.T) {
	script := os.Getenv("XKEEN_TEST_INSTALLED_APPLICATION")
	if script == "" {
		t.Skip("run XKEEN_LAB_INSTALLED=1 sh lab/check-agent-preparation.sh")
	}
	releaseEnrollment := make(chan struct{})
	defer func() {
		select {
		case <-releaseEnrollment:
		default:
			close(releaseEnrollment)
		}
	}()
	f := newRecoveryFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/agent/v1/enroll" {
			select {
			case <-releaseEnrollment:
			case <-r.Context().Done():
				return true
			}
		}
		return false
	})
	base, err := os.MkdirTemp("", "installed-application-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	if os.Chmod(base, 0755) != nil {
		t.Fatal("cannot make fixture accessible")
	}
	path := func(name string) string { return filepath.Join(base, name) }
	put := func(path string, data []byte, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"work", "socket", "journal", "stage", "release"} {
		mode := os.FileMode(0700)
		if name == "work" || name == "socket" {
			mode = 0755
		}
		if err := os.Mkdir(path(name), mode); err != nil {
			t.Fatal(err)
		}
	}
	ca, err := os.ReadFile(f.cert)
	if err != nil {
		t.Fatal(err)
	}
	put(path("ca"), ca, 0600)
	allow := localProbeEndpoint(t, "allow-installed")
	deny := localProbeEndpoint(t, "deny-installed")
	_, denyPort, _ := net.SplitHostPort(deny)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := listener.Addr().String()
	_, port, _ := net.SplitHostPort(proxy)
	listener.Close()
	initial := fixtureDesiredCatalog("{\"log\":{\"loglevel\":\"none\"}}")
	initial.Files[2].Content = []byte(fmt.Sprintf(`{"inbounds":[{"port":%s,"listen":"127.0.0.1","protocol":"socks","tag":"installed-socks"}]}`, port))
	initial.Files[3].Content = []byte(`{"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"block","protocol":"blackhole"}]}`)
	initial.Files[4].Content = []byte(fmt.Sprintf(`{"routing":{"rules":[{"type":"field","port":%q,"outboundTag":"block"}]}}`, denyPort))
	for _, file := range initial.Files {
		put(path("work/"+file.Name), file.Content, 0644)
	}
	xray := os.Getenv("XKEEN_TEST_XRAY")
	working := startProcess(t, xray, "run", "-confdir", path("work"))
	waitSocket(t, "tcp", proxy)
	tick, err := localvalidation.ProcessStartTick(working.cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	files, err := localvalidation.WorkingManifest(path("work"))
	if err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(xray)
	if err != nil {
		t.Fatal(err)
	}
	cfg := localvalidation.Config{Version: 1, Scope: preparation.Scope{Origin: f.origin, RouterID: recoveryRouter, Profile: configuration.XrayIdentityVersion, VerifierID: "installed-stable"},
		Socket: path("socket/validation.sock"), StageDirectory: path("stage"), CgroupDirectory: os.Getenv("XKEEN_TEST_CGROUP"),
		AgentUID: 65532, AgentGID: 65532, WorkerUID: 65534, WorkerGID: 65534,
		Xray: xray, XraySHA256: fmt.Sprintf("%x", sha256.Sum256(bin)), WorkingDirectory: path("work"), WorkingManifest: files,
		WorkingPID: working.cmd.Process.Pid, WorkingStartTick: tick, SOCKS: proxy, AllowURL: "http://" + allow, AllowMarker: "allow-installed", DenyURL: "http://" + deny, DenyMarker: "deny-installed"}
	app := localvalidation.ApplicationConfig{Version: 1, Mode: "direct-xray-lab", Socket: path("socket/application.sock"), JournalDirectory: path("journal"), CA: path("ca"), RuntimeUID: 65533, RuntimeGID: 65533, ReportVersion: 1}
	persist := func(file string, value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		put(path(file), raw, 0600)
	}
	persist("validator.json", cfg)
	persist("application.json", app)
	const tag = "v0.0.18"
	var manifest strings.Builder
	for name, source := range map[string]string{
		"xkeen-entware-installer-linux-arm64": "/proof/installer",
		"xkeen-agent-linux-arm64":             "/proof/xkeen-agent",
		"xkeen-local-validator-linux-arm64":   "/proof/xkeen-supervisor",
	} {
		raw, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		put(path("release/"+name), raw, 0600)
		fmt.Fprintf(&manifest, "%x  %s\n", sha256.Sum256(raw), name)
	}
	put(path("release/SHA256SUMS"), []byte(manifest.String()), 0600)
	mirrorCert, mirrorKey, _ := fixtureServerCertificate(t)
	mirror := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutPrefix(r.URL.Path, "/releases/download/"+tag+"/")
		if !ok || filepath.Base(name) != name {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, path("release/"+name))
	}))
	if err := startEntwareFixtureTLS(mirror, mirrorCert, mirrorKey); err != nil {
		t.Fatal(err)
	}
	defer mirror.Close()
	const initPath = "/opt/etc/init.d/S93xkeen-managed-agent"
	t.Cleanup(func() { _ = exec.Command(initPath, "stop").Run() })
	run := func(origin string) ([]byte, error) {
		t.Helper()
		return exec.CommandContext(t.Context(), "/opt/bin/sh", script, "install", "--role", "agent-application", "--version", tag,
			"--manifest-sha256", fmt.Sprintf("%x", sha256.Sum256([]byte(manifest.String()))), "--run-as", "65532:65532",
			"--server", origin, "--invitation-file", filepath.Join(f.dir, statefile.InvitationName),
			"--local-config", path("validator.json"), "--application-config", path("application.json"),
			"--release-mirror", mirror.URL, "--mirror-ca-file", mirrorCert).CombinedOutput()
	}
	if out, err := run("https://other.example"); err == nil || !strings.Contains(string(out), "managed application profile") {
		t.Fatalf("scope mismatch did not fail before activation: %v %s", err, out)
	}
	if _, err := os.Stat(initPath); !os.IsNotExist(err) {
		t.Fatal("scope mismatch installed a service")
	}
	// The assets were individually downloaded and checked by the previous
	// bootstrap invocation. Start the same privileged entry with an isolated
	// stage so we can interrupt only that actual updater process.
	stage, err := os.MkdirTemp("/opt/tmp", "installed-application-stage-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	for _, asset := range []string{"xkeen-entware-installer-linux-arm64", "xkeen-agent-linux-arm64", "xkeen-local-validator-linux-arm64"} {
		raw, err := os.ReadFile(path("release/" + asset))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0600)
		if asset == "xkeen-entware-installer-linux-arm64" {
			mode = 0700
		}
		put(filepath.Join(stage, asset), raw, mode)
	}
	manifestDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(manifest.String())))
	installing := exec.CommandContext(t.Context(), filepath.Join(stage, "xkeen-entware-installer-linux-arm64"),
		"install-application-stage", stage, tag, manifestDigest, "65532:65532", f.origin,
		filepath.Join(f.dir, statefile.InvitationName), path("validator.json"), path("application.json"))
	if err := installing.Start(); err != nil {
		t.Fatal(err)
	}
	defer installing.Process.Kill()
	const supervisorReceipt = "/opt/var/lib/xkeen-ui/install/agent-application-supervisor.process.json"
	const agentReceipt = "/opt/var/lib/xkeen-ui/install/agent-application.process.json"
	var readySupervisor []byte
	waitRecovery(t, 10*time.Second, "supervisor was not confirmed before agent confirmation", func() bool {
		supervisor, _ := os.ReadFile(supervisorReceipt)
		agent, _ := os.ReadFile(agentReceipt)
		var initial, child struct {
			PID     int
			Pending bool
		}
		if json.Unmarshal(supervisor, &initial) == nil && initial.PID > 1 && !initial.Pending &&
			json.Unmarshal(agent, &child) == nil && child.PID > 1 && child.Pending {
			readySupervisor = supervisor
			return true
		}
		return false
	})
	if err := installing.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := installing.Wait(); err == nil {
		t.Fatal("interrupted install returned success")
	}
	if _, err := os.Stat(initPath); !os.IsNotExist(err) {
		t.Fatal("unconfirmed installation registered init.d")
	}
	var supervisorProof struct{ PID int }
	if json.Unmarshal(readySupervisor, &supervisorProof) != nil {
		t.Fatal("invalid supervisor fixture receipt")
	}
	statusFile, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", supervisorProof.PID))
	if err != nil || !strings.Contains(string(statusFile), "NoNewPrivs:\t1") {
		t.Fatal("installed supervisor did not set no_new_privs")
	}

	journalBefore, err := os.ReadFile(path("journal/application.json"))
	if err != nil {
		t.Fatal(err)
	}
	foreign := exec.Command("/opt/bin/sleep", "120")
	if err := foreign.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = foreign.Process.Kill(); _ = foreign.Wait() }()
	foreignTick, err := localvalidation.ProcessStartTick(foreign.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	wrongReceipt, err := json.Marshal(map[string]any{"Boot": strings.TrimSpace(string(boot)), "PID": foreign.Process.Pid, "Tick": fmt.Sprint(foreignTick)})
	if err != nil {
		t.Fatal(err)
	}
	put(supervisorReceipt, wrongReceipt, 0600)
	out, err := run(f.origin)
	put(supervisorReceipt, readySupervisor, 0600)
	if err == nil || !strings.Contains(string(out), "instance ownership") || foreign.Process.Signal(syscall.Signal(0)) != nil {
		t.Fatalf("unowned supervisor was adopted or stopped: %v %s", err, out)
	}
	journalAfter, err := os.ReadFile(path("journal/application.json"))
	if err != nil || string(journalAfter) != string(journalBefore) {
		t.Fatal("unowned process retry changed application journal")
	}
	close(releaseEnrollment)
	if out, err := run(f.origin); err != nil {
		t.Fatalf("exact managed installation retry failed: %v %s", err, out)
	}
	after, err := os.ReadFile(supervisorReceipt)
	if err != nil || string(after) != string(readySupervisor) {
		t.Fatal("exact retry replaced verified supervisor")
	}

	invoke := func(action string) {
		t.Helper()
		if out, err := exec.CommandContext(t.Context(), initPath, action).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", action, err, out)
		}
	}
	invoke("status")
	if status := f.status(t); status.Applied != 0 {
		t.Fatal("transport falsely confirmed application")
	}
	before, err := f.store.Read(t.Context(), publication.ReadRequest{RouterID: cfg.Scope.RouterID})
	if err != nil {
		t.Fatal(err)
	}
	initial.Files[0].Content = []byte("{ \"log\": {\"loglevel\":\"warning\"} }\n")
	initial.Files[1].Content = []byte(`{"api":{"tag":"api","listen":"127.0.0.1:10085","services":["StatsService"]},"stats":{}}`)
	draft := publicationDraft(t, cfg.Scope.RouterID, before.TemplateID, initial.Files)
	service := publication.NewService(f.store, processPublicationValidator{xray: xray, dir: base})
	published, err := service.Publish(t.Context(), publication.PublishRequest{
		Mode: publication.PublishOrdinary, Source: publication.SourceCandidate, Candidate: &draft,
		Expected: publication.Expectations{RouterID: cfg.Scope.RouterID, TemplateID: before.TemplateID, AcceptedVersion: before.Accepted.Version,
			ActiveGeneration: before.Active.Generation, DesiredNumber: before.Desired.Number},
		OrderBasis: &publication.OrderBasis{TemplateID: before.TemplateID, TemplateVersion: before.Accepted.Version, SharedRuleIDs: []string{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitRecovery(t, 20*time.Second, "installed supervisor did not report proven application", func() bool { return f.status(t).Applied == published.Revision })
	invoke("stop")
	invoke("start")
	invoke("status")
	if f.status(t).Applied != published.Revision {
		t.Fatal("restart changed applied confirmation")
	}
}
