//go:build linux

package localvalidation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"golang.org/x/sys/unix"
)

const workerUnavailable = 125

// Worker is the trusted, short-lived stage already confined to a job cgroup
// and private namespaces by the supervisor. Only this process runs Xray.
func Worker(args []string) int {
	if len(args) != 8 || os.Getpid() != 1 {
		return workerUnavailable
	}
	jail, id, profile, tag, digest := args[0], args[1], args[3], args[4], args[7]
	number, err := strconv.ParseInt(args[2], 10, 64)
	uid, uidErr := strconv.ParseUint(args[5], 10, 32)
	gid, gidErr := strconv.ParseUint(args[6], 10, 32)
	if err != nil || uidErr != nil || gidErr != nil || uid == 0 || gid == 0 ||
		profile != configuration.XrayIdentityVersion || !agentrevision.ValidTag(tag) {
		return workerUnavailable
	}
	runtime.LockOSThread()
	if !confineRoot(jail, uid, gid) || !verifyStaged(id, profile, number, tag, digest) || !dropPrivileges(int(uid), int(gid)) {
		return workerUnavailable
	}
	env := []string{"XRAY_LOCATION_ASSET=/opt/etc/xray/dat"}
	version := exec.Command("/xray", "version")
	version.Env = env
	version.Stdin, version.Stderr = os.Stdin, os.Stderr
	output, err := version.Output()
	if err != nil || !strings.HasPrefix(string(output), "Xray "+profile+" ") {
		return workerUnavailable
	}
	if unix.CloseRange(3, ^uint(0), unix.CLOSE_RANGE_CLOEXEC) != nil {
		return workerUnavailable
	}
	_ = syscall.Exec("/xray", []string{"xray", "run", "-test", "-format", "json", "-confdir", "/candidate"}, env)
	return workerUnavailable
}

func confineRoot(jail string, uid, gid uint64) bool {
	if unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, "") != nil ||
		unix.Mount(jail, jail, "", unix.MS_BIND, "") != nil ||
		unix.Mount("", jail, "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY|unix.MS_NOSUID|unix.MS_NODEV, "") != nil {
		return false
	}
	log := jail + "/opt/var/log/xray"
	options := "size=1m,mode=0700,uid=" + strconv.FormatUint(uid, 10) + ",gid=" + strconv.FormatUint(gid, 10)
	if unix.Mount("tmpfs", log, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, options) != nil ||
		unix.Chroot(jail) != nil || os.Chdir("/") != nil {
		return false
	}
	return true
}

func verifyStaged(id, profile string, number int64, tag, digest string) bool {
	files, err := os.ReadDir("/candidate")
	if err != nil || len(files) < 5 || len(files) > 6 {
		return false
	}
	snapshot := agentrevision.Snapshot{Number: number, Profile: profile}
	left := int64(agentrevision.MaxCatalog)
	for _, f := range files {
		raw, err := readStaged("/candidate/"+f.Name(), left)
		if err != nil || len(raw) == 0 {
			return false
		}
		left -= int64(len(raw))
		snapshot.Files = append(snapshot.Files, configuration.CatalogFile{Name: f.Name(), Content: raw})
	}
	got, err := agentrevision.Tag(id, snapshot)
	if err != nil || got != tag {
		return false
	}
	binary, err := openRegular("/xray")
	if err != nil {
		return false
	}
	defer binary.Close()
	h := sha256.New()
	const maxBinary = 64 << 20
	n, err := io.Copy(h, io.LimitReader(binary, maxBinary+1))
	return err == nil && n > 0 && n <= maxBinary && hex.EncodeToString(h.Sum(nil)) == digest
}

func readStaged(path string, limit int64) ([]byte, error) {
	file, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errors.New("invalid staged file")
	}
	return raw, nil
}

func dropPrivileges(uid, gid int) bool {
	for _, resource := range []struct {
		kind  int
		limit uint64
	}{{unix.RLIMIT_NOFILE, 64}, {unix.RLIMIT_CORE, 0}, {unix.RLIMIT_CPU, 10}} {
		if unix.Setrlimit(resource.kind, &unix.Rlimit{Cur: resource.limit, Max: resource.limit}) != nil {
			return false
		}
	}
	for cap := uintptr(0); ; cap++ {
		err := unix.Prctl(unix.PR_CAPBSET_DROP, cap, 0, 0, 0)
		if errors.Is(err, unix.EINVAL) {
			break
		}
		if err != nil {
			return false
		}
	}
	var capabilities [2]unix.CapUserData
	if syscall.Setgroups(nil) != nil || syscall.Setgid(gid) != nil || syscall.Setuid(uid) != nil ||
		unix.Capset(&unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}, &capabilities[0]) != nil {
		return false
	}
	return unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) == nil
}
