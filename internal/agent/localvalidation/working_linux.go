//go:build linux

package localvalidation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"golang.org/x/sys/unix"
)

type Working struct {
	Directory, Manifest, VerifierID                   string
	PID                                               int
	StartTick                                         uint64
	BinaryDigest                                      [32]byte
	SOCKS, AllowURL, AllowMarker, DenyURL, DenyMarker string
}

func (w Working) Observe(ctx context.Context) (preparation.Baseline, error) {
	if w.PID < 1 || w.StartTick == 0 || w.Manifest == "" || w.VerifierID == "" || !w.identifyProcess() {
		return preparation.Baseline{}, preparation.ErrBaseline
	}
	files, manifest, err := workingFiles(w.Directory)
	if err != nil || manifest != w.Manifest || !w.checkTraffic(ctx) || !w.identifyProcess() {
		return preparation.Baseline{}, preparation.ErrBaseline
	}
	_, again, err := workingFiles(w.Directory)
	if err != nil || again != manifest || ctx.Err() != nil {
		return preparation.Baseline{}, preparation.ErrBaseline
	}
	return preparation.Baseline{Files: files, Runtime: preparation.Runtime{PID: w.PID, StartTick: w.StartTick, VerifierID: w.VerifierID}}, nil
}

func WorkingManifest(path string) (string, error) {
	_, id, err := workingFiles(path)
	return id, err
}

func workingFiles(path string) ([]preparation.BaselineFile, string, error) {
	if !privateDirectory(path) {
		return nil, "", preparation.ErrBaseline
	}
	dirFD, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", preparation.ErrBaseline
	}
	dir := os.NewFile(uintptr(dirFD), "working configuration directory")
	defer dir.Close()
	entries, err := dir.ReadDir(129)
	if err != nil && err != io.EOF || len(entries) > 128 {
		return nil, "", preparation.ErrBaseline
	}
	var files []preparation.BaselineFile
	remaining := int64(agentrevision.MaxCatalog)
	for _, e := range entries {
		switch e.Name() {
		case "01_log.json", "02_api.json", "03_inbounds.json", "04_outbounds.json", "05_routing.json", "06_extra.json":
			file, err := workingFile(dirFD, e.Name(), remaining)
			if err != nil {
				return nil, "", preparation.ErrBaseline
			}
			files = append(files, file)
			remaining -= int64(len(file.Content))
		default:
			switch strings.ToLower(filepath.Ext(e.Name())) {
			case ".json", ".jsonc", ".json5", ".toml", ".yaml", ".yml":
				return nil, "", preparation.ErrBaseline
			}
		}
	}
	if len(files) == 0 {
		return nil, "", preparation.ErrBaseline
	}
	slices.SortFunc(files, func(a, b preparation.BaselineFile) int { return strings.Compare(a.Name, b.Name) })
	manifest, err := json.Marshal(files)
	if err != nil {
		return nil, "", preparation.ErrBaseline
	}
	digest := sha256.Sum256(manifest)
	return files, hex.EncodeToString(digest[:]), nil
}

func workingFile(dir int, name string, limit int64) (preparation.BaselineFile, error) {
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return preparation.BaselineFile{}, err
	}
	f := os.NewFile(uintptr(fd), "working configuration file")
	defer f.Close()
	var before, after unix.Stat_t
	if err := unix.Fstat(fd, &before); err != nil || before.Mode&unix.S_IFMT != unix.S_IFREG ||
		before.Nlink != 1 || before.Uid != 0 || before.Mode&0022 != 0 || before.Size <= 0 || before.Size > limit {
		return preparation.BaselineFile{}, preparation.ErrBaseline
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) != before.Size || unix.Fstat(fd, &after) != nil ||
		before.Ctim != after.Ctim || before.Mtim != after.Mtim || before.Size != after.Size {
		return preparation.BaselineFile{}, preparation.ErrBaseline
	}
	return preparation.BaselineFile{Name: name, Content: raw, UID: before.Uid, GID: before.Gid, Mode: before.Mode & 07777}, nil
}

func ProcessStartTick(pid int) (uint64, error) {
	if pid < 1 {
		return 0, preparation.ErrBaseline
	}
	raw, err := readStaged(procPath(pid, "stat"), 4096)
	if err != nil {
		return 0, preparation.ErrBaseline
	}
	i := strings.LastIndexByte(string(raw), ')')
	if i < 0 {
		return 0, preparation.ErrBaseline
	}
	fields := strings.Fields(string(raw[i+1:]))
	if len(fields) < 20 || fields[0] == "Z" {
		return 0, preparation.ErrBaseline
	}
	tick, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || tick == 0 {
		return 0, preparation.ErrBaseline
	}
	return tick, nil
}

func procPath(pid int, name string) string {
	return filepath.Join("/proc", strconv.Itoa(pid), name)
}

func (w Working) identifyProcess() bool {
	before, err := ProcessStartTick(w.PID)
	if err != nil || before != w.StartTick {
		return false
	}
	raw, err := readStaged(procPath(w.PID, "cmdline"), 4096)
	if err != nil || len(raw) == 0 || raw[len(raw)-1] != 0 {
		return false
	}
	args := strings.Split(string(raw[:len(raw)-1]), "\x00")
	env, err := readStaged(procPath(w.PID, "environ"), 64<<10)
	if err != nil || !workingArguments(args[1:], env, w.Directory) {
		return false
	}
	local, localErr := os.Stat(w.Directory)
	visible, visibleErr := os.Stat(procPath(w.PID, "root") + w.Directory)
	if localErr != nil || visibleErr != nil || !os.SameFile(local, visible) {
		return false
	}
	// /proc/PID/exe is a kernel-managed magic link. Read the actual binary FD.
	f, err := os.Open(procPath(w.PID, "exe"))
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	const limit = 64 << 20
	if err != nil || !fi.Mode().IsRegular() || fi.Size() <= 0 || fi.Size() > limit {
		return false
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, limit+1))
	if err != nil || n != fi.Size() || !bytes.Equal(h.Sum(nil), w.BinaryDigest[:]) {
		return false
	}
	after, err := ProcessStartTick(w.PID)
	return err == nil && after == before
}

func workingArguments(args []string, rawEnv []byte, dir string) bool {
	env := make(map[string]string)
	for _, field := range strings.Split(string(rawEnv), "\x00") {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		key = strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
		switch key {
		case "XRAY_LOCATION_CONFDIR", "XRAY_LOCATION_CONFIG", "XRAY_LOCATION_ASSET":
			if _, duplicate := env[key]; duplicate {
				return false
			}
			env[key] = value
		}
	}
	if env["XRAY_LOCATION_CONFIG"] != "" || (env["XRAY_LOCATION_ASSET"] != "" && env["XRAY_LOCATION_ASSET"] != "/opt/etc/xray/dat") ||
		(env["XRAY_LOCATION_CONFDIR"] != "" && env["XRAY_LOCATION_CONFDIR"] != dir) {
		return false
	}
	return slices.Equal(args, []string{"run", "-confdir", dir}) ||
		slices.Equal(args, []string{"run", "-format", "json", "-confdir", dir}) ||
		(slices.Equal(args, []string{"run"}) && env["XRAY_LOCATION_CONFDIR"] == dir)
}

func (w Working) checkTraffic(ctx context.Context) bool {
	allow, denied := localTarget(w.AllowURL), localTarget(w.DenyURL)
	if allow == nil || denied == nil || allow.Host == denied.Host || !loopbackHost(w.SOCKS) ||
		len(w.AllowMarker) == 0 || len(w.AllowMarker) > 256 || len(w.DenyMarker) == 0 || len(w.DenyMarker) > 256 {
		return false
	}
	proxy := &url.URL{Scheme: "socks5", Host: w.SOCKS}
	direct := probeClient(nil)
	defer direct.CloseIdleConnections()
	proxied := probeClient(http.ProxyURL(proxy))
	defer proxied.CloseIdleConnections()
	return probe(ctx, direct, denied, w.DenyMarker) == probeOK &&
		probe(ctx, proxied, allow, w.AllowMarker) == probeOK &&
		probe(ctx, proxied, denied, w.DenyMarker) == probeFailed &&
		probe(ctx, proxied, allow, w.AllowMarker) == probeOK &&
		probe(ctx, direct, denied, w.DenyMarker) == probeOK
}

func localTarget(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || !loopbackHost(u.Host) {
		return nil
	}
	return u
}

func loopbackHost(host string) bool {
	ip, port, err := net.SplitHostPort(host)
	if err != nil {
		return false
	}
	address, err := netip.ParseAddr(ip)
	n, pErr := strconv.ParseUint(port, 10, 16)
	return err == nil && pErr == nil && n > 0 && address.IsLoopback()
}

func probeClient(proxy func(*http.Request) (*url.URL, error)) *http.Client {
	return &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: proxy, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type probeResult uint8

const (
	probeUnknown probeResult = iota
	probeOK
	probeFailed
)

func probe(ctx context.Context, client *http.Client, u *url.URL, marker string) probeResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return probeUnknown
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return probeUnknown
		}
		return probeFailed
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 257))
	if err != nil || resp.StatusCode != http.StatusOK || string(raw) != marker {
		return probeUnknown
	}
	return probeOK
}
