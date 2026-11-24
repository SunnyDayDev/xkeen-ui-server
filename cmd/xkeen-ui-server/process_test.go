package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

var testBinary string
var projectRoot string

func TestMain(m *testing.M) {
	code := func() int {
		if binary := os.Getenv("XKEEN_TEST_SERVER_BINARY"); binary != "" {
			testBinary = binary
			return m.Run()
		}
		dir, err := os.MkdirTemp("", "xkeen-server-test-")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer os.RemoveAll(dir)
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		projectRoot = filepath.Clean(filepath.Join(cwd, "../.."))
		testBinary = filepath.Join(dir, "xkeen-ui-server")
		// Match the launcher build so its readiness check starts with a warm Go cache.
		build := exec.Command(filepath.Join(runtime.GOROOT(), "bin/go"), "build", "-trimpath", "-o", testBinary, ".")
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if output, err := build.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build test binary: %v\n%s", err, output)
			return 1
		}
		return m.Run()
	}()
	os.Exit(code)
}

func TestProcessSignals(t *testing.T) {
	for _, signal := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			p := startProcess(t, testBinary, "--listen", "127.0.0.1:0")
			address := p.ready(t)
			if err := p.cmd.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			if err := p.wait(t); err != nil {
				t.Fatalf("exit: %v; %s", err, p.output())
			}
			listener, err := net.Listen("tcp", address)
			if err != nil {
				t.Fatalf("port not released: %v", err)
			}
			_ = listener.Close()
		})
	}
}

func TestProcessIPv6(t *testing.T) {
	probe, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 unavailable: %v", err)
	}
	_ = probe.Close()
	p := startProcess(t, testBinary, "--listen", "[::1]:0")
	address := p.ready(t)
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "::1" {
		t.Fatalf("IPv6 address = %q, %v", address, err)
	}
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := p.wait(t); err != nil {
		t.Fatal(err)
	}
}

func TestProcessInvalidArgs(t *testing.T) {
	for _, args := range [][]string{
		{"--unknown"}, {"positional"}, {"--listen", ""},
		{"--listen", "127.0.0.1"}, {"--listen", "localhost:8080"},
		{"--listen", "127.0.0.1:65536"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, stderr, code := runBinary(t, args...)
			if code != 2 || stderr == "" {
				t.Errorf("exit=%d stderr=%q, want 2 and diagnostic", code, stderr)
			}
			if strings.Contains(stderr, `"listening"`) {
				t.Error("reported startup for invalid arguments")
			}
		})
	}
}

func TestProcessBusyPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, stderr, code := runBinary(t, "--listen", listener.Addr().String())
	if code != 1 || stderr == "" {
		t.Errorf("exit=%d stderr=%q, want 1 and diagnostic", code, stderr)
	}
	if strings.Contains(stderr, `"listening"`) {
		t.Error("reported startup after bind failure")
	}
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("original listener affected: %v", err)
	}
	_ = conn.Close()
}

func TestProcessHelp(t *testing.T) {
	stdout, stderr, code := runBinary(t, "--help")
	if code != 0 || stderr != "" {
		t.Errorf("exit=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "listen") || !strings.Contains(stdout, "127.0.0.1:8080") {
		t.Errorf("incomplete help: %q", stdout)
	}
}

func runBinary(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return runBinaryInDir(t, t.TempDir(), args...)
}

func runBinaryInDir(t *testing.T, dir string, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, testBinary, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("command did not exit: %v", ctx.Err())
	}
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return stdout.String(), stderr.String(), exit.ExitCode()
		}
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), 0
}

type process struct {
	cmd     *exec.Cmd
	done    chan struct{}
	err     error
	logPath string
}

func startProcess(t *testing.T, command string, args ...string) *process {
	t.Helper()
	dir := projectRoot
	if command == testBinary {
		dir = t.TempDir()
	}
	return startProcessInDir(t, dir, command, args...)
}

func startProcessInDir(t *testing.T, dir, command string, args ...string) *process {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "process.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(runtime.GOROOT(), "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	p := &process{cmd: cmd, done: make(chan struct{}), logPath: logPath}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			_ = cmd.Process.Kill()
			select {
			case <-p.done:
			case <-time.After(3 * time.Second):
				t.Error("process cleanup timed out")
			}
		}
		_ = log.Close()
	})
	return p
}

func (p *process) output() string {
	data, _ := os.ReadFile(p.logPath)
	return string(data)
}

func (p *process) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-p.done:
		return p.err
	case <-time.After(7 * time.Second):
		t.Fatal("process did not exit")
		return nil
	}
}

func (p *process) ready(t *testing.T) string {
	t.Helper()
	client := &http.Client{Timeout: 200 * time.Millisecond, Transport: &http.Transport{DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			t.Fatalf("process exited before ready: %v; %s", p.err, p.output())
		default:
		}
		for _, line := range strings.Split(p.output(), "\n") {
			var event struct {
				Event   string `json:"event"`
				Address string `json:"address"`
			}
			if json.Unmarshal([]byte(line), &event) != nil || event.Event != "listening" {
				continue
			}
			response, err := client.Get("http://" + event.Address + "/healthz")
			if err != nil {
				continue
			}
			data, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			var body map[string]string
			if err == nil && response.StatusCode == 200 && response.Header.Get("Content-Type") == "application/json" && json.Unmarshal(data, &body) == nil && len(body) == 1 && body["status"] == "ok" {
				return event.Address
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("health did not become available; %s", p.output())
	return ""
}

func TestProcessStorageRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state", "xkeen-ui-server.sqlite")
	launch := func() *process {
		started := time.Now()
		p := startProcessInDir(t, dir, testBinary, "--listen", "127.0.0.1:0")
		p.ready(t)
		t.Logf("startup through /healthz: %s", time.Since(started))
		return p
	}
	stop := func(p *process) {
		t.Helper()
		if runtime.GOOS == "linux" {
			statusPath := fmt.Sprintf("/proc/%d/status", p.cmd.Process.Pid)
			status, err := os.ReadFile(statusPath)
			if err != nil {
				t.Logf("peak resident memory unavailable: %v", err)
			}
			for line := range strings.SplitSeq(string(status), "\n") {
				if strings.HasPrefix(line, "VmHWM:") {
					t.Log(line)
				}
			}
		}
		if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		if err := p.wait(t); err != nil {
			t.Fatalf("exit: %v; %s", err, p.output())
		}
	}
	p := launch()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE probe (value TEXT NOT NULL); INSERT INTO probe VALUES ('demo.example')"); err != nil {
		t.Fatal(err)
	}
	history := storageText(t, db, "SELECT group_concat(version) FROM (SELECT version FROM schema_migrations ORDER BY version)")
	stop(p)
	p = launch()
	if got := storageText(t, db, "SELECT value FROM probe"); got != "demo.example" {
		t.Errorf("stored value = %q", got)
	}
	if got := storageText(t, db, "SELECT group_concat(version) FROM (SELECT version FROM schema_migrations ORDER BY version)"); got != history {
		t.Errorf("migration history = %q", got)
	}
	stop(p)
}

func TestProcessStorageFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "newer.sqlite")
	db, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO schema_migrations SELECT max(version) + 1 FROM schema_migrations"); err != nil {
		t.Fatal(err)
	}

	version := storageText(t, db, "SELECT max(version) FROM schema_migrations")
	history := storageText(t, db, "SELECT group_concat(version) FROM (SELECT version FROM schema_migrations ORDER BY version)")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	for _, test := range []struct {
		path string
		want string
	}{
		{dir, "open SQLite"},
		{path, "migrate SQLite: schema version " + version},
	} {
		_, stderr, code := runBinary(t, "--db-path", test.path, "--listen", listener.Addr().String())
		if code != 1 || !strings.Contains(stderr, test.want) || strings.Contains(stderr, "listening") {
			t.Errorf("exit=%d stderr=%q, want storage failure %q before bind", code, stderr, test.want)
		}
	}
	if got := storageText(t, db, "SELECT group_concat(version) FROM (SELECT version FROM schema_migrations ORDER BY version)"); got != history {
		t.Errorf("migration history changed: %q", got)
	}
}

func TestProcessStorageHelpAndInvalidArgs(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"--help"}, {"--listen", "invalid"}, {"--db-path", ""}} {
		runBinaryInDir(t, dir, args...)
		if _, err := os.Stat(filepath.Join(dir, "state")); !os.IsNotExist(err) {
			t.Fatalf("%q touched default storage: %v", args, err)
		}
	}
}

func storageText(t *testing.T, db *sql.DB, statement string) string {
	t.Helper()
	var value string
	if err := db.QueryRow(statement).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
