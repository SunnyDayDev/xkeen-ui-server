package validation

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
)

var imageID = regexp.MustCompile("^sha256:[0-9a-f]{64}$")
var jobName = regexp.MustCompile("^job-[a-f0-9]{32}$")

type dockerState struct {
	Status, Error string
	ExitCode      int
	OOMKilled     bool
}

func classify(s dockerState) error {
	if s.Status != "exited" || s.OOMKilled || s.Error != "" {
		return publication.ErrValidatorUnavailable
	}
	switch s.ExitCode {
	case 0:
		return nil
	case 23:
		return publication.ErrCatalogRejected
	default:
		return publication.ErrValidatorUnavailable
	}
}

// DockerWorker owns a dedicated, private staging directory on the Docker host.
type DockerWorker struct {
	binary, socket, image, root, label string
	lock                               *os.File
}

func NewDockerWorker(binary, socket, image, root string) (*DockerWorker, error) {
	if !filepath.IsAbs(binary) || !filepath.IsAbs(socket) || !filepath.IsAbs(root) || !imageID.MatchString(image) {
		return nil, publication.ErrValidatorUnavailable
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, publication.ErrValidatorUnavailable
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return nil, publication.ErrValidatorUnavailable
	}
	lock, err := os.OpenFile(filepath.Join(root, "worker.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, publication.ErrValidatorUnavailable
	}
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		lock.Close()
		return nil, publication.ErrValidatorUnavailable
	}
	hash := sha256.Sum256([]byte(root))
	w := &DockerWorker{binary, socket, image, root, fmt.Sprintf("xkeen.validation=%x", hash[:12]), lock}
	if err := w.recover(); err != nil {
		w.Close()
		return nil, err
	}
	return w, nil
}
func (w *DockerWorker) Close() error          { return w.lock.Close() }
func (w *DockerWorker) Handler() http.Handler { return newHandler(w) }

type cappedOutput struct {
	mu     sync.Mutex
	data   bytes.Buffer
	over   bool
	cancel context.CancelFunc
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.over {
		return len(p), nil
	}
	if len(p) > 64*1024-b.data.Len() {
		b.over = true
		b.cancel()
		return len(p), nil
	}
	return b.data.Write(p)
}
func (w *DockerWorker) cli(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, w.binary, append([]string{"--host", "unix://" + w.socket}, args...)...)
	// In particular do not forward ambient proxies, Docker contexts or credentials.
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + w.root, "DOCKER_CONFIG=" + w.root}
	cmd.WaitDelay = time.Second
	out := &cappedOutput{cancel: cancel}
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	if err != nil || out.over || ctx.Err() != nil {
		return nil, publication.ErrValidatorUnavailable
	}
	return out.data.Bytes(), nil
}

func (w *DockerWorker) recover() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	caps, err := w.cli(ctx, "info", "--format", "{{.MemoryLimit}} {{.SwapLimit}} {{.PidsLimit}} {{.CPUCfsQuota}} {{json .SecurityOptions}}")
	if err != nil || !strings.HasPrefix(string(caps), "true true true true ") || !strings.Contains(string(caps), "name=seccomp") {
		return publication.ErrValidatorUnavailable
	}
	raw, err := w.cli(ctx, "ps", "-aq", "--no-trunc", "--filter", "label="+w.label)
	if err != nil {
		return err
	}
	for _, id := range strings.Fields(string(raw)) {
		if len(id) != 64 {
			return publication.ErrValidatorUnavailable
		}
		if _, err := hex.DecodeString(id); err != nil {
			return publication.ErrValidatorUnavailable
		}
		if _, err := w.cli(ctx, "rm", "-f", id); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(w.root)
	if err != nil {
		return publication.ErrValidatorUnavailable
	}
	for _, entry := range entries {
		if jobName.MatchString(entry.Name()) {
			if err := os.RemoveAll(filepath.Join(w.root, entry.Name())); err != nil {
				return publication.ErrValidatorUnavailable
			}
		}
	}
	return nil
}

func (w *DockerWorker) check(ctx context.Context, req request) (result error) {
	// A single slot and process lock give recovery exclusive ownership of stale jobs.
	if err := w.recover(); err != nil {
		return err
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return publication.ErrValidatorUnavailable
	}
	dir := filepath.Join(w.root, "job-"+hex.EncodeToString(token))
	if err := os.Mkdir(dir, 0700); err != nil {
		return publication.ErrValidatorUnavailable
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			result = publication.ErrValidatorUnavailable
		}
	}()
	// The unmounted parent remains 0700; only the read-only child is exposed.
	folder := filepath.Join(dir, "candidate")
	if err := os.Mkdir(folder, 0755); err != nil {
		return publication.ErrValidatorUnavailable
	}
	for _, f := range req.Files {
		p := filepath.Join(folder, f.Name)
		if err := os.WriteFile(p, f.Bytes, 0444); err != nil {
			return publication.ErrValidatorUnavailable
		}
		if err := os.Chmod(p, 0444); err != nil {
			return publication.ErrValidatorUnavailable
		}
		read, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(read, f.Bytes) {
			return publication.ErrValidatorUnavailable
		}
	}
	if err := os.Chmod(folder, 0555); err != nil {
		return publication.ErrValidatorUnavailable
	}
	defer os.Chmod(folder, 0700)
	version, state, err := w.run(ctx, dir, "version")
	if err != nil || classify(state) != nil {
		return publication.ErrValidatorUnavailable
	}
	fields := strings.Fields(string(version))
	if len(fields) < 2 || fields[0] != "Xray" || fields[1] != req.Profile {
		return publication.ErrValidatorUnavailable
	}
	out, state, err := w.run(ctx, dir, "test")
	if err != nil {
		return err
	}
	if err := classify(state); err != nil {
		// A positive configuration rejection is meaningful only after successful startup.
		// Ambiguous input/asset I/O or resource errors stay unavailable.
		if errors.Is(err, publication.ErrCatalogRejected) && !ambiguousExit(out) {
			return err
		}
		return publication.ErrValidatorUnavailable
	}
	if !bytes.HasSuffix(bytes.TrimSpace(out), []byte("Configuration OK.")) {
		return publication.ErrValidatorUnavailable
	}
	return nil
}
func ambiguousExit(out []byte) bool {
	// Recognize explicit parser/build diagnoses from the pinned binary. Default closed
	// for new or ambiguous diagnostics. Never return process output to a caller.
	last := strings.TrimSpace(string(out))
	i := strings.LastIndex(last, " > ")
	if i < 0 {
		return true
	}
	cause := last[i+3:]
	if !strings.Contains(last, "Failed to start: main: failed to ") {
		return true
	}
	// Trust closed categories of configuration failures. A leaf reporting file I/O,
	// a system error, or a future category is not proof of an invalid candidate.
	for _, prefix := range []string{"infra/conf: unknown config id: ", "infra/conf: invalid ", "infra/conf: unknown protocol", "infra/conf: unsupported ", "infra/conf: unrecognized ", "infra/conf: no rules", "infra/conf: empty ", "common/uuid: invalid "} {
		if strings.HasPrefix(cause, prefix) {
			return false
		}
	}
	return true
}
func (w *DockerWorker) run(ctx context.Context, dir string, args ...string) (out []byte, state dockerState, result error) {
	name := "xkeen-validation-" + filepath.Base(dir) + "-" + args[0]
	mount := "type=bind,src=" + filepath.Join(dir, "candidate") + ",dst=/candidate,readonly"
	argv := []string{"create", "--pull=never", "--name", name, "--label", w.label, "--network", "none", "--read-only", "--user", "65534:65534", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--memory", "128m", "--memory-swap", "128m", "--cpus", "0.5", "--pids-limit", "48", "--ulimit", "nofile=128:128", "--ulimit", "core=0:0", "--log-driver", "none", "--ipc", "none", "--tmpfs", "/opt/var/log/xray:rw,nosuid,nodev,noexec,size=1m,uid=65534,gid=65534,mode=0700", "--mount", mount, "--entrypoint", "/check", w.image}
	// Cleanup uses a separate bounded context even after request cancellation.
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if _, err := w.cli(clean, "rm", "-f", name); err != nil {
			result = publication.ErrValidatorUnavailable
		}
	}()
	if _, err := w.cli(ctx, append(argv, args...)...); err != nil {
		return nil, state, err
	}
	// start -a propagates Xray's 23. Keep bounded stdout while classifying via inspect.
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(ctx, w.binary, "--host", "unix://"+w.socket, "start", "-a", name)
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + w.root, "DOCKER_CONFIG=" + w.root}
	cmd.WaitDelay = time.Second
	buf := &cappedOutput{cancel: stop}
	cmd.Stdout = buf
	cmd.Stderr = buf
	runErr := cmd.Run()
	if ctx.Err() != nil || buf.over {
		return nil, state, publication.ErrValidatorUnavailable
	}
	raw, err := w.cli(ctx, "inspect", "--format", "{{json .State}}", name)
	if err != nil || json.Unmarshal(raw, &state) != nil {
		return nil, state, publication.ErrValidatorUnavailable
	}
	if runErr != nil {
		var exit *exec.ExitError
		if !errors.As(runErr, &exit) || exit.ExitCode() != state.ExitCode {
			return nil, state, publication.ErrValidatorUnavailable
		}
	}
	return buf.data.Bytes(), state, nil
}
