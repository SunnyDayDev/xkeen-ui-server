package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/localvalidation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/restart"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

const g18Template = "laboratory-xray-example"

type installedFlow struct {
	t                             *testing.T
	base, exchange, origin        string
	agentOrigin, mirror, manifest string
}

func TestInstalledEntwareOwnerFlow(t *testing.T) {
	exchange := os.Getenv("XKEEN_G18_EXCHANGE")
	if exchange == "" {
		t.Skip("run lab/check-installed-entware-flow.sh")
	}
	work, err := os.MkdirTemp("", "xkeen-g18-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(work)
	if err := os.Chmod(work, 0755); err != nil {
		t.Fatal(err)
	}
	f := installedFlow{t: t, base: work, exchange: exchange, origin: "https://127.0.0.1:" + os.Getenv("XKEEN_G18_UI_PORT")}
	central := "127.0.0.1:" + os.Getenv("XKEEN_G18_UI_PORT")
	link := newInstalledAgentLink(t, central)
	f.agentOrigin = link.origin()
	for _, name := range []string{"server-cert.pem", "server-key.pem", "worker-ca.pem", "worker-client.pem", "worker-client-key.pem"} {
		raw, err := os.ReadFile(filepath.Join(exchange, name))
		if err != nil {
			t.Fatal("synthetic TLS input unavailable")
		}
		f.write(f.path(name), raw, 0600)
	}
	f.startMirror()
	f.install("server", "--run-as", "65531:65531", "--listen", "0.0.0.0:"+os.Getenv("XKEEN_G18_UI_PORT"),
		"--web-origin", f.origin, "--agent-origin", f.agentOrigin, "--tls-cert", f.path("server-cert.pem"),
		"--tls-key", f.path("server-key.pem"), "--validator-origin", os.Getenv("XKEEN_G18_WORKER_ORIGIN"),
		"--validator-ca", f.path("worker-ca.pem"), "--validator-client-cert", f.path("worker-client.pem"),
		"--validator-client-key", f.path("worker-client-key.pem"))
	const serverInit = "/opt/etc/init.d/S91xkeen-ui-server"
	t.Cleanup(func() { _ = exec.Command(serverInit, "stop").Run() })
	const dbPath = "/opt/var/lib/xkeen-ui/server/server.sqlite"
	pass := "/opt/var/lib/xkeen-ui/server/laboratory-password"
	const password = "synthetic G18 administrator password"
	f.write(pass, []byte(password+"\n"), 0600)
	if err := os.Chown(pass, 65531, 65531); err != nil {
		t.Fatal(err)
	}
	f.command(serverInit, "admin", "setup", "--password-file", pass)
	if err := os.Remove(pass); err != nil {
		t.Fatal(err)
	}

	allow := localProbeEndpoint(t, "g18-allow")
	deny := localProbeEndpoint(t, "g18-deny")
	witness := localProbeEndpoint(t, "g18-witness")
	free, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := free.Addr().String()
	free.Close()
	f.emit("fixture", map[string]string{
		"origin": f.origin, "agentOrigin": f.agentOrigin, "templateId": g18Template, "proxyPort": portOf(proxy),
		"denyPort": portOf(deny), "witnessPort": portOf(witness),
	})
	t.Log("installed HTTPS server, F02 administrator, and configured external worker")

	var enrollmentInput struct {
		RouterID string
	}
	f.wait("router", 80*time.Second, &enrollmentInput)
	raw, err := os.ReadFile(filepath.Join(exchange, "invitation"))
	if err != nil || len(raw) != 69 {
		t.Fatal("one-time invitation file unavailable")
	}
	if err := os.Remove(filepath.Join(exchange, "invitation")); err != nil {
		t.Fatal(err)
	}
	f.write(f.path("invitation"), raw, 0600)
	clear(raw)
	routerID := enrollmentInput.RouterID
	f.prepareRuntime(routerID, proxy, allow, deny)
	f.install("agent-application", "--run-as", "65532:65532", "--server", f.agentOrigin,
		"--invitation-file", f.path("invitation"), "--local-config", f.path("validator.json"),
		"--application-config", f.path("application.json"))
	if err := os.Remove(f.path("invitation")); err != nil {
		t.Fatal(err)
	}
	const agentInit = "/opt/etc/init.d/S93xkeen-managed-agent"
	t.Cleanup(func() { _ = exec.Command(agentInit, "stop").Run() })
	db, err := sqlite.OpenExisting(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	contacts := sqlite.NewContacts(db, time.Now)
	status := func() reporting.Status {
		t.Helper()
		value, err := contacts.Status(t.Context(), routerID)
		if err != nil {
			t.Fatal("stored contact evidence unavailable")
		}
		return value
	}
	waitApplied := func(number int64) reporting.Status {
		t.Helper()
		var value reporting.Status
		var emitted string
		waitRecovery(t, 75*time.Second, "no verified E08 application", func() bool {
			value = status()
			kind, stage, code := "", "", ""
			if value.Last != nil {
				kind, stage, code = value.Last.State, value.Last.Stage, value.Last.Code
			}
			phase := fmt.Sprintf("contact=%s desired=%d applied=%d runtime=%s result=%s stage=%s code=%s",
				value.Presence, value.Desired, value.Applied, value.Runtime, kind, stage, code)
			if phase != emitted {
				t.Log(phase)
				emitted = phase
			}
			return value.Desired == number && value.Applied == number && value.Runtime == "verified" &&
				value.Last != nil && value.Last.State == "applied" && value.Last.Revision == number
		})
		return value
	}
	first := waitApplied(1)
	firstReceipt := f.committed(1, first.Last.Sequence)
	f.traffic(proxy, allow, "g18-allow", true)
	f.traffic("", deny, "g18-deny", true)
	f.traffic(proxy, deny, "", false)
	f.traffic(proxy, witness, "g18-witness", true)
	firstKey := f.identityProof(routerID)
	f.emit("first", map[string]any{"applied": first.Applied, "reportSequence": first.Last.Sequence})
	t.Log("first UI publication is verified in runtime and acknowledged once")

	link.change(false)
	waitRecovery(t, reporting.OfflineAfter+12*time.Second, "ordinary contact did not reach offline", func() bool {
		value := status()
		return value.Presence == reporting.Offline && value.Desired == 1 && value.Applied == 1
	})
	f.emit("offline", true)
	f.wait("published", 55*time.Second, nil)
	held := status()
	if held.Presence != reporting.Offline || held.Desired != 3 || held.Applied != 1 || held.Last == nil ||
		held.Last.Sequence != first.Last.Sequence || f.committed(1, first.Last.Sequence).AttemptID != firstReceipt.AttemptID {
		t.Fatal("offline publications changed applied evidence")
	}
	f.traffic(proxy, witness, "g18-witness", true)
	link.change(true)
	final := waitApplied(3)
	receipt := f.committed(3, final.Last.Sequence)
	if final.Last.Sequence != first.Last.Sequence+1 || receipt.AttemptID == firstReceipt.AttemptID {
		t.Fatal("latest generation did not get exactly one new report")
	}
	f.assertPersonal(sqlite.NewPublications(db), routerID, portOf(witness))
	f.traffic(proxy, allow, "g18-allow", true)
	f.traffic("", deny, "g18-deny", true)
	f.traffic(proxy, deny, "", false)
	f.traffic(proxy, witness, "", false)
	t.Log("agent resumed with the latest of two offline publications and changed synthetic Xray traffic")

	f.command(agentInit, "stop")
	f.command(agentInit, "start")
	f.command(agentInit, "status")
	restarted := waitApplied(3)
	if restarted.Last.Sequence != final.Last.Sequence || f.committed(3, final.Last.Sequence).AttemptID != receipt.AttemptID {
		t.Fatal("service restart duplicated an application result")
	}
	if f.identityProof(routerID) != firstKey {
		t.Fatal("restart changed the working key")
	}
	f.traffic(proxy, witness, "", false)
	f.emit("complete", map[string]any{"routerId": routerID, "desired": final.Desired, "applied": final.Applied, "reportSequence": final.Last.Sequence})
	f.wait("browser-complete", 20*time.Second, nil)
}

func (f installedFlow) path(name string) string { return filepath.Join(f.base, name) }

func (f installedFlow) write(path string, value []byte, mode os.FileMode) {
	f.t.Helper()
	if err := os.WriteFile(path, value, mode); err != nil {
		f.t.Fatal("cannot create synthetic fixture file")
	}
}

func (f installedFlow) emit(name string, value any) {
	f.t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		f.t.Fatal(err)
	}
	target := filepath.Join(f.exchange, name+".json")
	f.write(target+".tmp", raw, 0600)
	if err := os.Rename(target+".tmp", target); err != nil {
		f.t.Fatal("cannot publish fixture milestone")
	}
}

func (f installedFlow) wait(name string, max time.Duration, out any) {
	f.t.Helper()
	waitRecovery(f.t, max, "browser milestone "+name+" did not arrive", func() bool {
		raw, err := os.ReadFile(filepath.Join(f.exchange, name+".json"))
		return err == nil && (out == nil || json.Unmarshal(raw, out) == nil)
	})
}

func (f installedFlow) command(path string, args ...string) {
	f.t.Helper()
	cmd := exec.CommandContext(f.t.Context(), path, args...)
	if raw, err := cmd.CombinedOutput(); err != nil {
		f.t.Fatalf("synthetic service command failed: %v: %s", err, raw)
	}
}

func (f *installedFlow) startMirror() {
	f.t.Helper()
	names := []string{"xkeen-ui-server-linux-arm64", "xkeen-agent-linux-arm64", "xkeen-local-validator-linux-arm64", "xkeen-entware-installer-linux-arm64", "agent-state-format.txt", "install-entware.sh"}
	assets := map[string]bool{}
	for _, name := range names {
		assets[name] = true
	}
	sums, err := os.ReadFile("/proof/SHA256SUMS")
	if err != nil {
		f.t.Fatal("G16 bundle manifest unavailable")
	}
	f.manifest = fmt.Sprintf("%x", sha256.Sum256(sums))
	script, err := os.ReadFile("/proof/install-entware.sh")
	ownerProof, proofErr := os.ReadFile("/proof/owner-verification.txt")
	scriptHash := fmt.Sprintf("%x", sha256.Sum256(script))
	if err != nil || proofErr != nil || !strings.Contains(string(ownerProof), "installer_sha256 "+scriptHash+"\n") ||
		!strings.Contains(string(ownerProof), "manifest_sha256 "+f.manifest+"\n") {
		f.t.Fatal("G16 bootstrap verification failed")
	}
	mirror := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutPrefix(r.URL.Path, "/releases/download/v0.0.141/")
		switch {
		case ok && name == "SHA256SUMS":
			_, _ = w.Write(sums)
		case ok && assets[name]:
			http.ServeFile(w, r, filepath.Join("/proof", name))
		default:
			http.NotFound(w, r)
		}
	}))
	if err := startEntwareFixtureTLS(mirror, f.path("server-cert.pem"), f.path("server-key.pem")); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(mirror.Close)
	f.mirror = mirror.URL
}

func (f installedFlow) install(role string, extra ...string) {
	f.t.Helper()
	args := []string{"/proof/install-entware.sh", "install", "--role", role, "--version", "v0.0.141",
		"--manifest-sha256", f.manifest, "--release-mirror", f.mirror, "--mirror-ca-file", f.path("server-cert.pem")}
	f.command("/opt/bin/sh", append(args, extra...)...)
}

func (f installedFlow) prepareRuntime(id, proxy, allow, deny string) {
	f.t.Helper()
	for _, name := range []string{"working", "socket", "journal"} {
		mode := os.FileMode(0700)
		if name == "working" || name == "socket" {
			mode = 0755
		}
		if err := os.Mkdir(f.path(name), mode); err != nil {
			f.t.Fatal(err)
		}
	}
	// The disposable worker has to execute the verified Xray copy. The lab
	// uses a separate root-owned, executable Entware directory for that job.
	stage, err := os.MkdirTemp("/opt/tmp", "g18-validation-")
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = os.RemoveAll(stage) })
	catalog := fixtureDesiredCatalog(`{"log":{"loglevel":"none"}}`)
	catalog.Files[2].Content = []byte(fmt.Sprintf(`{"inbounds":[{"port":%s,"listen":"127.0.0.1","protocol":"socks","tag":"g18-socks"}]}`, portOf(proxy)))
	catalog.Files[3].Content = []byte(`{"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"block","protocol":"blackhole"}]}`)
	catalog.Files[4].Content = []byte(fmt.Sprintf(`{"routing":{"rules":[{"type":"field","port":%q,"outboundTag":"block"}]}}`, portOf(deny)))
	for _, file := range catalog.Files {
		f.write(f.path("working/"+file.Name), file.Content, 0644)
	}
	xray := os.Getenv("XKEEN_TEST_XRAY")
	working := startProcess(f.t, xray, "run", "-confdir", f.path("working"))
	waitSocket(f.t, "tcp", proxy)
	tick, err := localvalidation.ProcessStartTick(working.cmd.Process.Pid)
	if err != nil {
		f.t.Fatal(err)
	}
	manifest, err := localvalidation.WorkingManifest(f.path("working"))
	if err != nil {
		f.t.Fatal(err)
	}
	binary, err := os.ReadFile(xray)
	if err != nil {
		f.t.Fatal(err)
	}
	cfg := localvalidation.Config{Version: 1, Scope: preparation.Scope{Origin: f.agentOrigin, RouterID: id, Profile: configuration.XrayIdentityVersion, VerifierID: "installed-g18"},
		Socket: f.path("socket/validation.sock"), StageDirectory: stage, CgroupDirectory: os.Getenv("XKEEN_TEST_CGROUP"),
		AgentUID: 65532, AgentGID: 65532, WorkerUID: 65534, WorkerGID: 65534, Xray: xray, XraySHA256: fmt.Sprintf("%x", sha256.Sum256(binary)),
		WorkingDirectory: f.path("working"), WorkingManifest: manifest, WorkingPID: working.cmd.Process.Pid, WorkingStartTick: tick,
		SOCKS: proxy, AllowURL: "http://" + allow, AllowMarker: "g18-allow", DenyURL: "http://" + deny, DenyMarker: "g18-deny"}
	app := localvalidation.ApplicationConfig{Version: 1, Mode: "direct-xray-lab", Socket: f.path("socket/application.sock"),
		JournalDirectory: f.path("journal"), CA: f.path("server-cert.pem"), RuntimeUID: 65533, RuntimeGID: 65533, ReportVersion: 1}
	for name, value := range map[string]any{"validator.json": cfg, "application.json": app} {
		raw, err := json.Marshal(value)
		if err != nil {
			f.t.Fatal(err)
		}
		f.write(f.path(name), raw, 0600)
	}
}

func (f installedFlow) traffic(proxy, address, marker string, allowed bool) {
	f.t.Helper()
	transport := &http.Transport{DisableKeepAlives: true}
	if proxy != "" {
		transport.Proxy = http.ProxyURL(&url.URL{Scheme: "socks5", Host: proxy})
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	response, err := client.Get("http://" + address)
	if response != nil {
		defer response.Body.Close()
	}
	if !allowed {
		if err == nil {
			f.t.Fatal("Xray allowed denied synthetic traffic")
		}
		return
	}
	if err != nil || response.StatusCode != 200 {
		f.t.Fatal("Xray denied permitted synthetic traffic")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, int64(len(marker)+1)))
	if err != nil || string(raw) != marker {
		f.t.Fatal("unexpected synthetic response")
	}
}

func (f installedFlow) committed(number, sequence int64) restart.Confirmed {
	f.t.Helper()
	var record restart.Record
	waitRecovery(f.t, 6*time.Second, "durable E08 report and attempt differ", func() bool {
		raw, err := os.ReadFile(f.path("journal/application.json"))
		return err == nil && json.Unmarshal(raw, &record) == nil && record.Current != nil && record.Current.Number == number &&
			record.Delivery != nil && record.Delivery.Done && record.Delivery.Sequence == sequence && record.Delivery.Outcome != nil &&
			record.Delivery.Outcome.AttemptID == record.Current.AttemptID
	})
	return *record.Current
}

func (f installedFlow) identityProof(id string) [32]byte {
	f.t.Helper()
	raw, err := os.ReadFile("/opt/var/lib/xkeen-ui/agent-application/credentials.json")
	var state struct{ State enrollment.State }
	if err != nil || json.Unmarshal(raw, &state) != nil || state.State.RouterID != id || state.State.Origin != f.agentOrigin || state.State.Key == "" {
		f.t.Fatal("working enrollment scope changed")
	}
	clear(raw)
	return sha256.Sum256([]byte(state.State.Key))
}

func (f installedFlow) assertPersonal(store *sqlite.Publications, id, witness string) {
	f.t.Helper()
	value, err := store.Read(f.t.Context(), publication.ReadRequest{RouterID: id})
	if err != nil || value.Active == nil || value.Desired == nil || value.Desired.Number != 3 {
		f.t.Fatal("published personal scope unavailable")
	}
	for _, group := range value.Active.Draft.Groups {
		for _, override := range group.Overrides {
			var port string
			if raw := override.Values["witness.port"]; json.Unmarshal(raw, &port) == nil && port == witness {
				return
			}
		}
	}
	f.t.Fatal("personal value was lost during group publication")
}

func portOf(address string) string {
	_, port, _ := net.SplitHostPort(address)
	return port
}
