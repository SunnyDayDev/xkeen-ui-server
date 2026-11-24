package main

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/agenthttp"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
)

const (
	stateBase      = "/opt/var/lib/xkeen-ui"
	stateDir       = stateBase + "/agent-transport"
	control        = stateBase + "/install"
	profileDir     = "/opt/etc/xkeen-ui/agent-transport"
	profilePath    = profileDir + "/profile.json"
	executableBase = "/opt/libexec/xkeen-ui/agent-transport"
	current        = executableBase + "/current"
	initPath       = "/opt/etc/init.d/S92xkeen-agent"
	agentAsset     = "xkeen-agent-linux-arm64"
	installerAsset = "xkeen-entware-installer-linux-arm64"
)

var (
	errUsage     = errors.New("invalid Entware installation command")
	errSafety    = errors.New("unsafe or unavailable Entware installation path")
	errInstalled = errors.New("a transport agent installation already exists")
	tagPattern   = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	shaPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type profile struct {
	Version  string
	Manifest string
	Origin   string
	UID      uint32
	GID      uint32
	CADigest string
}

func run(args []string) error {
	if os.Geteuid() != 0 || len(args) == 0 {
		return errUsage
	}
	switch {
	case args[0] == "install-stage" && len(args) == 8:
		return install(args[1:])
	case len(args) == 1 && (args[0] == "start" || args[0] == "stop" || args[0] == "status"):
		lock, err := lockService()
		if err != nil {
			return err
		}
		defer lock.Close()
		if err := rootDirs(false, profileDir, executableBase, executableBase+"/generations"); err != nil {
			return err
		}
		p, err := readProfile()
		if err != nil {
			return err
		}
		return lifecycle(args[0], p)
	default:
		return errUsage
	}
}

func install(args []string) error {
	stage, version, manifest, principal, origin, caPath, invitation := args[0], args[1], args[2], args[3], args[4], args[5], args[6]
	uid, gid, ok := parsePrincipal(principal)
	if !ok || len(version) > 40 || !tagPattern.MatchString(version) || !shaPattern.MatchString(manifest) {
		return errUsage
	}
	client, err := agenthttp.New(origin, nil)
	if err != nil {
		return errors.New("invalid HTTPS agent origin")
	}
	defer client.CloseIdleConnections()
	p := profile{Version: version, Manifest: manifest, Origin: client.Origin(), UID: uid, GID: gid}
	ca, err := readCA(caPath)
	if err != nil {
		return err
	}
	if len(ca) != 0 {
		p.CADigest = fmt.Sprintf("%x", sha256.Sum256(ca))
	}
	stageInfo, err := os.Lstat(stage)
	if err != nil || filepath.Dir(stage) != "/opt/tmp" || !owned(stageInfo, 0, os.ModeDir, 0700) {
		return errSafety
	}
	if err := probePIDFD(); err != nil {
		return errors.New("Entware kernel cannot identify managed processes")
	}
	lock, err := lockService()
	if err != nil {
		return err
	}
	defer lock.Close()
	if _, err := os.Lstat(initPath); !errors.Is(err, os.ErrNotExist) {
		return errInstalled
	}
	if err := rootDirs(true, profileDir, executableBase, executableBase+"/generations", filepath.Dir(initPath)); err != nil {
		return err
	}
	prior, err := readProfile()
	if err == nil && prior != p {
		return errors.New("retained installation requires the same release and service profile")
	}
	hasPrior := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errSafety
	}
	invite, err := stateAdmission(p, invitation, hasPrior)
	if err != nil {
		return err
	}
	if err := persistProfile(p, ca, hasPrior); err != nil {
		return err
	}
	if err := prepareState(p, invite); err != nil {
		return err
	}
	published, err := existingGeneration(p, hasPrior)
	if err != nil {
		return err
	}
	if !published {
		if err := prepareGeneration(stage, p); err != nil {
			return err
		}
		if os.Symlink("generations/"+p.Version, current) != nil || syncDir(executableBase) != nil {
			return errSafety
		}
	}
	if err := lifecycle("start", p); err != nil {
		if lifecycle("stop", p) != nil || removePublished(current) != nil {
			return errors.New("agent start failed and its installation needs local recovery")
		}
		return err
	}
	// Automatic startup is registered only once the owned agent has confirmed
	// readiness and the corresponding process receipt is durable.
	if err := writeFile(initPath, []byte(initScript), 0755, 0, 0); err != nil {
		if lifecycle("stop", p) == nil {
			_ = errors.Join(removePublished(initPath), removePublished(current))
		}
		return errors.New("cannot publish agent init registration")
	}

	fmt.Println("Entware agent installation ready")
	return nil
}

func existingGeneration(p profile, hasPrior bool) (bool, error) {
	target, err := os.Readlink(current)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !hasPrior || target != "generations/"+p.Version {
		return false, errInstalled
	}
	if err := syncDir(executableBase); err != nil {
		return false, errSafety
	}
	return true, nil
}

func parsePrincipal(s string) (uint32, uint32, bool) {
	u, g, ok := strings.Cut(s, ":")
	uid, errU := strconv.ParseUint(u, 10, 31)
	gid, errG := strconv.ParseUint(g, 10, 31)
	return uint32(uid), uint32(gid), ok && errU == nil && errG == nil && uid != 0 && gid != 0
}

func stateAdmission(p profile, path string, hasPrior bool) ([]byte, error) {
	stateErr := errors.New("agent state or invitation is unavailable")
	info, err := os.Lstat(stateDir)
	if err == nil {
		if !hasPrior || !owned(info, p.UID, os.ModeDir, 0700) || info.Sys().(*syscall.Stat_t).Gid != p.GID {
			return nil, stateErr
		}
		return nil, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, stateErr
	}
	file, err := checkedFile(path, 0, 0600)
	if err != nil {
		return nil, stateErr
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, agentcredential.Size+3))
	if err != nil || len(raw) > agentcredential.Size+2 {
		return nil, stateErr
	}
	token := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if _, ok := agentcredential.Parse(token, agentcredential.Invitation); !ok {
		return nil, stateErr
	}
	return raw, nil
}

func prepareState(p profile, invite []byte) error {
	if len(invite) == 0 {
		return nil
	}
	pending, err := os.MkdirTemp(stateBase, ".agent-state-")
	if err != nil {
		return errSafety
	}
	defer os.RemoveAll(pending)
	if err := writeFile(filepath.Join(pending, statefile.InvitationName), invite, 0600, int(p.UID), int(p.GID)); err != nil {
		return err
	}
	if os.Chown(pending, int(p.UID), int(p.GID)) != nil || syncDir(pending) != nil {
		return errSafety
	}
	if os.Rename(pending, stateDir) != nil || syncDir(stateBase) != nil {
		return errSafety
	}
	return nil
}

func readCA(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	file, err := checkedFile(path, 0, 0)
	if err != nil {
		return nil, errors.New("agent CA file is unavailable")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 || !x509.NewCertPool().AppendCertsFromPEM(raw) {
		return nil, errors.New("agent CA file is invalid")
	}
	return raw, nil
}

func persistProfile(p profile, ca []byte, hasPrior bool) error {
	if hasPrior {
		return verifyProfileCA(p)
	}
	if len(ca) != 0 {
		if err := writeFile(profileDir+"/ca.pem", ca, 0644, 0, 0); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return errSafety
	}
	return writeFile(profilePath, raw, 0600, 0, 0)
}

func verifyProfileCA(p profile) error {
	if p.CADigest == "" {
		return nil
	}
	ca, err := readCA(profileDir + "/ca.pem")
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(ca)) != p.CADigest {
		return errors.New("installed agent CA binding does not match the retained profile")
	}
	return nil
}

func readProfile() (profile, error) {
	file, err := checkedFile(profilePath, 0, 0600)
	if err != nil {
		return profile{}, err
	}
	defer file.Close()
	var p profile
	raw, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil || len(raw) > 8192 || jsonv2.Unmarshal(raw, &p, jsonv2.RejectUnknownMembers(true)) != nil ||
		!tagPattern.MatchString(p.Version) || len(p.Version) > 40 || !shaPattern.MatchString(p.Manifest) ||
		p.UID == 0 || p.GID == 0 || p.Origin == "" || (p.CADigest != "" && !shaPattern.MatchString(p.CADigest)) {
		return profile{}, errSafety
	}
	return p, nil
}

func prepareGeneration(stage string, p profile) error {
	dir := executableBase + "/generations/" + p.Version
	if err := rootDirs(true, dir); err != nil {
		return err
	}
	for _, name := range []string{agentAsset, installerAsset} {
		in, err := checkedFile(filepath.Join(stage, name), 0, 0)
		if err != nil {
			return errSafety
		}
		raw, err := io.ReadAll(io.LimitReader(in, (64<<20)+1))
		in.Close()
		if err != nil || len(raw) == 0 || len(raw) > 64<<20 {
			return errSafety
		}
		if err := writeFile(filepath.Join(dir, name), raw, 0755, 0, 0); err != nil {
			return err
		}
	}
	return nil
}

const initScript = `#!/opt/bin/sh
set -eu
[ "$#" -ge 1 ] && [ "$#" -le 2 ] || exit 2
case "$1" in start|stop|status) action=$1 ;; check) action=status ;; *) exit 2 ;; esac
exec /opt/libexec/xkeen-ui/agent-transport/current/xkeen-entware-installer-linux-arm64 "$action"
`
