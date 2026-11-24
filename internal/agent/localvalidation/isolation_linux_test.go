//go:build linux

package localvalidation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"golang.org/x/sys/unix"
)

type syntheticJob struct {
	Marker, Address string
	Wait            bool
}

func TestRunnerCannotReachHostAndCancelsAllJobProcesses(t *testing.T) {
	if os.Getenv("XKEEN_TEST_LOCAL_VALIDATOR") == "" {
		t.Skip("requires disposable delegated cgroup and pinned launcher")
	}
	host, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	dir := t.TempDir()
	marker := filepath.Join(dir, "host-only")
	if err := os.WriteFile(marker, []byte("synthetic host marker"), 0644); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	run := Runner{StageDir: dir, CgroupDir: os.Getenv("XKEEN_TEST_CGROUP"), Binary: binary, Digest: sha256.Sum256(raw), UID: 65534, GID: 65534}
	candidate := validatorFixture(t, os.Getenv("XKEEN_TEST_CATALOG"))
	input := syntheticJob{Marker: marker, Address: host.Addr().String()}
	candidate.Files[0].Content, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Run(t.Context(), "one.example", candidate); err != nil {
		t.Fatalf("isolated synthetic worker could not prove boundary: %v", err)
	}
	input.Wait = true
	candidate.Files[0].Content, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run.Run(ctx, "one.example", candidate) }()
	started := false
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
		entries, err := os.ReadDir(run.CgroupDir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() && !jobEmpty(filepath.Join(run.CgroupDir, entry.Name())) {
				started = true
			}
		}
		if started {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !started {
		t.Fatal("synthetic job never became populated")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, preparation.ErrUnavailable) {
			t.Fatalf("canceled job=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled job was not joined")
	}
	entries, err := os.ReadDir(run.CgroupDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatal("canceled validation left a cgroup")
		}
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("validation touched host data")
	}
}

func syntheticXray(args []string) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Println("Xray 26.3.27 synthetic isolation probe")
		return 0
	}
	if strings.Join(args, " ") != "run -test -format json -confdir /candidate" {
		return 2
	}
	raw, err := os.ReadFile("/candidate/01_log.json")
	if err != nil {
		return 3
	}
	var input syntheticJob
	if json.Unmarshal(raw, &input) != nil || input.Marker == "" || input.Address == "" {
		return 4
	}
	if input.Wait {
		time.Sleep(time.Minute)
		return 5
	}
	if _, err := os.Stat(input.Marker); !errors.Is(err, os.ErrNotExist) {
		return 6
	}
	if _, err := os.Stat("/proc"); !errors.Is(err, os.ErrNotExist) {
		return 7
	}
	if verifierFD, err := os.Open("/candidate/01_log.json"); err != nil {
		return 8
	} else {
		verifierFD.Close()
	}
	if os.WriteFile("/candidate/01_log.json", []byte("changed"), 0600) == nil {
		return 9
	}
	if conn, err := net.DialTimeout("tcp", input.Address, 250*time.Millisecond); err == nil {
		conn.Close()
		return 10
	}
	groups, err := os.Getgroups()
	if err != nil || len(groups) != 0 || os.Geteuid() != 65534 || os.Getegid() != 65534 {
		return 11
	}
	flags, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil || flags != 1 {
		return 12
	}
	caps := [2]unix.CapUserData{}
	if unix.Capget(&unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}, &caps[0]) != nil ||
		caps != ([2]unix.CapUserData{}) {
		return 13
	}
	if err := os.WriteFile("/opt/var/log/xray/small", []byte("temporary"), 0600); err != nil {
		return 14
	}
	if err := os.WriteFile("/opt/var/log/xray/too-large", make([]byte, 2<<20), 0600); !errors.Is(err, unix.ENOSPC) {
		return 15
	}
	return 0
}
