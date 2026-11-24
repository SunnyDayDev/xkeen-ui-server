//go:build linux

package localvalidation

import (
	"context"
	"crypto/x509"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/agenthttp"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/restart"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

// ApplicationConfig grants authority for a dedicated direct Xray service.
type ApplicationConfig struct {
	Version                      int
	Mode                         string
	Socket, JournalDirectory, CA string
	RuntimeUID, RuntimeGID       uint32
}

func LoadApplicationConfig(path string, validator Config) (ApplicationConfig, error) {
	file, err := protectedApplicationFile(path, 64<<10)
	if err != nil {
		return ApplicationConfig{}, err
	}
	defer file.Close()
	var config ApplicationConfig
	if !readJSON(file, &config) || config.Version != 1 || config.Mode != "direct-xray-lab" ||
		config.RuntimeUID == 0 || config.RuntimeGID == 0 ||
		filepath.Clean(config.Socket) != config.Socket || !privateDirectory(filepath.Dir(config.Socket)) ||
		config.Socket == validator.Socket || !privateDirectory(config.JournalDirectory) ||
		overlap(config.JournalDirectory, validator.WorkingDirectory) ||
		overlap(config.JournalDirectory, validator.StageDirectory) ||
		overlap(config.JournalDirectory, validator.CgroupDirectory) ||
		overlap(config.JournalDirectory, filepath.Dir(config.Socket)) ||
		overlap(filepath.Dir(config.Socket), validator.WorkingDirectory) {
		return ApplicationConfig{}, restart.ErrJournal
	}
	return config, nil
}

func (config ApplicationConfig) OpenCentral(scope preparation.Scope) (*agenthttp.Client, error) {
	file, err := protectedApplicationFile(config.CA, 1<<20)
	if err != nil {
		return nil, restart.ErrDesired
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	roots := x509.NewCertPool()
	if err != nil || len(raw) > 1<<20 || !roots.AppendCertsFromPEM(raw) {
		return nil, restart.ErrDesired
	}
	return agenthttp.New(scope.Origin, roots)
}

func protectedApplicationFile(path string, limit int64) (*os.File, error) {
	if os.Geteuid() != 0 || filepath.Clean(path) != path || !privateDirectory(filepath.Dir(path)) {
		return nil, restart.ErrJournal
	}
	file, err := openRegular(path)
	if err != nil {
		return nil, restart.ErrJournal
	}
	info, err := file.Stat()
	if err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if ok && stat.Uid == 0 && stat.Nlink == 1 && info.Mode().Perm() == 0600 && info.Size() > 0 && info.Size() <= limit {
			return file, nil
		}
	}
	file.Close()
	return nil, restart.ErrJournal
}

func (r Runner) Check(ctx context.Context, scope preparation.Scope, snapshot agentrevision.Snapshot) error {
	return r.Run(ctx, scope.RouterID, snapshot)
}
