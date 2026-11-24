//go:build linux

package localvalidation

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type Config struct {
	Version                                           int
	Scope                                             preparation.Scope
	Socket, StageDirectory, CgroupDirectory           string
	AgentUID, AgentGID, WorkerUID, WorkerGID          uint32
	Xray, XraySHA256                                  string
	WorkingDirectory, WorkingManifest                 string
	WorkingPID                                        int
	WorkingStartTick                                  uint64
	SOCKS, AllowURL, AllowMarker, DenyURL, DenyMarker string
}

func LoadConfig(path string) (Config, error) {
	if os.Geteuid() != 0 || filepath.Clean(path) != path || !privateDirectory(filepath.Dir(path)) {
		return Config{}, preparation.ErrUnavailable
	}
	f, err := openRegular(path)
	if err != nil {
		return Config{}, preparation.ErrUnavailable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Mode().Perm() != 0600 || info.Size() <= 0 || info.Size() > 64<<10 {
		return Config{}, preparation.ErrUnavailable
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || stat.Nlink != 1 {
		return Config{}, preparation.ErrUnavailable
	}
	var config Config
	if !readJSON(f, &config) || !config.valid() {
		return Config{}, preparation.ErrUnavailable
	}
	return config, nil
}

func (c Config) valid() bool {
	_, binaryOK := decodeDigest(c.XraySHA256)
	_, manifestOK := decodeDigest(c.WorkingManifest)
	return runtime.GOARCH == "arm64" && c.Version == 1 && c.Scope.Origin != "" && c.Scope.RouterID != "" && c.Scope.VerifierID != "" &&
		c.Scope.Profile == configuration.XrayIdentityVersion && c.AgentUID > 0 && c.AgentGID > 0 &&
		c.WorkerUID > 0 && c.WorkerUID != c.AgentUID && c.WorkerGID > 0 && c.WorkerGID != c.AgentGID &&
		c.WorkingPID > 0 && c.WorkingStartTick > 0 && binaryOK && manifestOK &&
		filepath.IsAbs(c.Xray) && filepath.IsAbs(c.Socket) && filepath.IsAbs(c.StageDirectory) &&
		filepath.IsAbs(c.CgroupDirectory) && filepath.IsAbs(c.WorkingDirectory) &&
		!overlap(c.WorkingDirectory, c.StageDirectory) && !overlap(c.WorkingDirectory, filepath.Dir(c.Socket)) &&
		!overlap(c.WorkingDirectory, c.CgroupDirectory)

}

func (c Config) Runner() Runner {
	digest, _ := decodeDigest(c.XraySHA256)
	return Runner{StageDir: c.StageDirectory, CgroupDir: c.CgroupDirectory, Binary: c.Xray, Digest: digest, UID: c.WorkerUID, GID: c.WorkerGID}
}

func (c Config) Observer() Working {
	digest, _ := decodeDigest(c.XraySHA256)
	return Working{Directory: c.WorkingDirectory, Manifest: c.WorkingManifest, VerifierID: c.Scope.VerifierID,
		PID: c.WorkingPID, StartTick: c.WorkingStartTick, BinaryDigest: digest, SOCKS: c.SOCKS,
		AllowURL: c.AllowURL, AllowMarker: c.AllowMarker, DenyURL: c.DenyURL, DenyMarker: c.DenyMarker}
}

func decodeDigest(value string) ([32]byte, bool) {
	var out [32]byte
	if len(value) != hex.EncodedLen(len(out)) {
		return out, false
	}
	n, err := hex.Decode(out[:], []byte(value))
	return out, err == nil && n == len(out) && out != ([32]byte{}) && value == hex.EncodeToString(out[:])
}

func overlap(first, second string) bool {
	includes := func(base, path string) bool {
		rel, err := filepath.Rel(base, path)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
	}
	return includes(first, second) || includes(second, first)
}
