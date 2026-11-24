package main

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/agenthttp"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/localvalidation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
)

const (
	applicationBase              = "/opt/libexec/xkeen-ui/agent-application"
	applicationCurrent           = applicationBase + "/current"
	applicationConfigDir         = "/opt/etc/xkeen-ui/agent-application"
	applicationProfile           = applicationConfigDir + "/profile.json"
	applicationCA                = applicationConfigDir + "/ca.pem"
	applicationState             = stateBase + "/agent-application"
	applicationInit              = "/opt/etc/init.d/S93xkeen-managed-agent"
	applicationAgentReceipt      = control + "/agent-application.process.json"
	applicationSupervisorReceipt = control + "/agent-application-supervisor.process.json"
	supervisorAsset              = "xkeen-local-validator-linux-arm64"
)

var errApplication = errors.New("managed application profile is unavailable or does not match the installation")

type applicationProfileRecord struct {
	Agent                  profile
	WorkerUID, WorkerGID   uint32
	RuntimeUID, RuntimeGID uint32
	RouterID               string
	Verifier               string
	Socket                 string
	LocalPath              string
	LocalDigest            string
	ApplicationPath        string
	ApplicationDigest      string
}

func installApplication(args []string) error {
	stage, version, manifest, principal, origin, invitation, localPath, appPath := args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7]
	uid, gid, ok := parsePrincipal(principal)
	if !ok || !trustedStage(stage) || !validRelease(release{version, manifest}) {
		return errUsage
	}
	client, err := agenthttp.New(origin, nil)
	if err != nil {
		return errApplication
	}
	defer client.CloseIdleConnections()
	sources, err := readApplicationSources(localPath, appPath)
	cfg, app := sources.local, sources.app
	if err != nil || cfg.Scope.Origin != client.Origin() || cfg.AgentUID != uid || cfg.AgentGID != gid {
		return errApplication
	}
	p := applicationProfileRecord{
		Agent:     profile{Version: version, Manifest: manifest, Origin: client.Origin(), UID: uid, GID: gid, CADigest: digest(sources.ca)},
		WorkerUID: cfg.WorkerUID, WorkerGID: cfg.WorkerGID, RuntimeUID: app.RuntimeUID, RuntimeGID: app.RuntimeGID,
		RouterID: cfg.Scope.RouterID, Verifier: cfg.Scope.VerifierID, Socket: app.Socket,
		LocalPath: localPath, LocalDigest: digest(sources.localRaw), ApplicationPath: appPath, ApplicationDigest: digest(sources.appRaw),
	}
	if probePIDFD() != nil {
		return errApplication
	}
	admission, err := lockAdmission("agent-application", uid, gid)
	if err != nil {
		return err
	}
	defer admission.Close()
	serverUID, serverGID, err := otherPrincipal("agent-application")
	if err != nil || p.overlapsPrincipal(serverUID, serverGID) {
		return errors.New("agent and server require separate service identities")
	}

	lock, err := lockRole("agent-application")
	if err != nil {
		return err
	}
	defer lock.Close()
	if _, err := os.Lstat(profilePath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("managed application profile requires an independent installation")
	}
	if _, err := os.Lstat(applicationInit); !errors.Is(err, os.ErrNotExist) {
		return errInstalled
	}
	if err := rootDirs(true, applicationConfigDir, applicationBase+"/generations", filepath.Dir(applicationInit)); err != nil {
		return err
	}
	prior, err := loadApplicationProfile()
	hasPrior := err == nil
	if (hasPrior && prior != p) || (err != nil && !errors.Is(err, os.ErrNotExist)) {
		return errApplication
	}
	if !hasPrior {
		if _, err := os.Lstat(filepath.Join(app.JournalDirectory, "application.json")); !errors.Is(err, os.ErrNotExist) {
			return errApplication
		}
		// Check the one-time attestation without starting Xray or substituting a PID.
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		_, err := cfg.Observer().Observe(ctx)
		cancel()
		if err != nil {
			return errApplication
		}
	}
	token, err := stateAdmission(applicationState, p.Agent, invitation, hasPrior)
	if err != nil {
		return err
	}
	if !hasPrior {
		if err := writeFile(applicationCA, sources.ca, 0644, 0, 0); err != nil {
			return err
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			return errApplication
		}
		if err := writeFile(applicationProfile, encoded, 0600, 0, 0); err != nil {
			return err
		}
	}
	if err := prepareState(applicationState, p.Agent, token); err != nil {
		return err
	}
	target := "generations/" + p.Agent.Version
	got, err := os.Readlink(applicationCurrent)
	if errors.Is(err, os.ErrNotExist) {
		dir := filepath.Join(applicationBase, target)
		if err := rootDirs(true, dir); err != nil {
			return err
		}
		for _, name := range []string{installerAsset, agentAsset, supervisorAsset} {
			raw, err := readProtected(filepath.Join(stage, name), 0, 64<<20)
			if err != nil || len(raw) == 0 {
				return errSafety
			}
			if err := writeFile(filepath.Join(dir, name), raw, 0755, 0, 0); err != nil {
				return err
			}
		}
		if os.Symlink(target, applicationCurrent) != nil {
			return errSafety
		}
	} else if err != nil || !hasPrior || got != target {
		return errInstalled
	}
	if syncDir(applicationBase) != nil {
		return errSafety
	}
	if err := p.control("start", true); err != nil {
		if p.control("stop", true) == nil {
			_ = removePublished(applicationCurrent)
		}
		return err
	}
	if err := writeFile(applicationInit, []byte(applicationInitScript), 0755, 0, 0); err != nil {
		if p.control("stop", true) == nil {
			_ = errors.Join(removePublished(applicationInit), removePublished(applicationCurrent))
		}
		return err
	}
	fmt.Println("Entware managed agent service registered")
	return nil
}

func loadApplicationProfile() (applicationProfileRecord, error) {
	raw, err := readProtected(applicationProfile, 0600, 16384)
	if err != nil {
		return applicationProfileRecord{}, err
	}
	var p applicationProfileRecord
	if jsonv2.Unmarshal(raw, &p, jsonv2.RejectUnknownMembers(true)) != nil ||
		!validRelease(release{p.Agent.Version, p.Agent.Manifest}) || p.Agent.UID == 0 || p.Agent.GID == 0 ||
		p.WorkerUID == 0 || p.WorkerGID == 0 || p.RuntimeUID == 0 || p.RuntimeGID == 0 || p.RouterID == "" || p.Verifier == "" || !shaPattern.MatchString(p.Agent.CADigest) {
		return applicationProfileRecord{}, errApplication
	}
	return p, nil
}

func (p applicationProfileRecord) overlapsPrincipal(uid, gid uint32) bool {
	return p.Agent.UID == uid || p.WorkerUID == uid || p.RuntimeUID == uid ||
		p.Agent.GID == gid || p.WorkerGID == gid || p.RuntimeGID == gid
}

type applicationSources struct {
	local                localvalidation.Config
	app                  localvalidation.ApplicationConfig
	localRaw, appRaw, ca []byte
}

func readApplicationSources(localPath, appPath string) (applicationSources, error) {
	var sources applicationSources
	var err error
	sources.localRaw, err = readProtected(localPath, 0600, 64<<10)
	if err != nil {
		return applicationSources{}, errApplication
	}
	sources.local, err = localvalidation.LoadConfig(localPath)
	if err != nil {
		return applicationSources{}, errApplication
	}
	sources.appRaw, err = readProtected(appPath, 0600, 64<<10)
	if err != nil {
		return applicationSources{}, errApplication
	}
	sources.app, err = localvalidation.LoadApplicationConfig(appPath, sources.local)
	if err != nil || sources.app.ReportVersion != 1 {
		return applicationSources{}, errApplication
	}
	sources.ca, err = readCA(sources.app.CA)
	if err != nil || len(sources.ca) == 0 {
		return applicationSources{}, errApplication
	}
	trust, err := sources.app.OpenCentral(sources.local.Scope)
	if err != nil {
		return applicationSources{}, errApplication
	}
	trust.CloseIdleConnections()
	var info unix.Statfs_t
	if unix.Statfs(sources.local.CgroupDirectory, &info) != nil || info.Type != unix.CGROUP2_SUPER_MAGIC {
		return applicationSources{}, errApplication
	}
	return sources, nil
}

func applicationService(action string) error {
	if action != "start" && action != "stop" && action != "status" {
		return errUsage
	}
	lock, err := lockRole("agent-application")
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := rootDirs(false, applicationConfigDir, applicationBase); err != nil {
		return err
	}
	p, err := loadApplicationProfile()
	if err != nil {
		return err
	}
	return p.control(action, false)
}

func (p applicationProfileRecord) control(action string, first bool) error {
	target, err := os.Readlink(applicationCurrent)
	if err != nil || target != "generations/"+p.Agent.Version {
		return errApplication
	}
	dir := filepath.Join(applicationBase, target)
	supervisor := managedProcess{binary: filepath.Join(dir, supervisorAsset), receipt: applicationSupervisorReceipt,
		arguments:    []string{"--config", p.LocalPath, "--application-config", p.ApplicationPath},
		useReadyPipe: true, noNewPrivileges: true, notReady: errApplication,
		readiness: func(line string) bool {
			return line == "local application ready" || (!first && line == "local reporting ready; startup blocked by terminal rollback")
		},
		probe: func(pid int) bool { return ownsApplicationSocket(p.Socket, pid) },
	}
	agent := managedProcess{binary: filepath.Join(dir, agentAsset), receipt: applicationAgentReceipt, uid: p.Agent.UID, gid: p.Agent.GID,
		useReadyPipe: true, notReady: errNotReady,
		arguments: []string{"--server", p.Agent.Origin, "--expected-router-id", p.RouterID, "--state-dir", applicationState, "--ca-file", applicationCA,
			"--local-application-socket", p.Socket, "--local-verifier", p.Verifier, "--local-application-reports"},
	}
	if action == "stop" {
		if err := agent.control("stop"); err != nil {
			return err
		}
		return supervisor.control("stop")
	}
	sources, err := readApplicationSources(p.LocalPath, p.ApplicationPath)
	cfg, app := sources.local, sources.app
	if err != nil || p.RouterID != cfg.Scope.RouterID || p.Verifier != cfg.Scope.VerifierID || p.Agent.Origin != cfg.Scope.Origin ||
		p.Agent.UID != cfg.AgentUID || p.Agent.GID != cfg.AgentGID ||
		p.WorkerUID != cfg.WorkerUID || p.WorkerGID != cfg.WorkerGID || p.RuntimeUID != app.RuntimeUID || p.RuntimeGID != app.RuntimeGID || app.Socket != p.Socket || digest(sources.localRaw) != p.LocalDigest ||
		digest(sources.appRaw) != p.ApplicationDigest || digest(sources.ca) != p.Agent.CADigest {
		return errApplication
	}
	copied, err := readProtected(applicationCA, 0644, 1<<20)
	if err != nil || digest(copied) != p.Agent.CADigest {
		return errApplication
	}
	if action == "status" {
		if err := supervisor.control("status"); err != nil {
			return err
		}
		if err := agent.control("status"); err != nil {
			return err
		}
		return p.checkBinding()
	}
	if err := recoverOwnedChild(supervisor); err != nil {
		return err
	}
	if err := recoverOwnedChild(agent); err != nil {
		return err
	}
	return p.checkBinding()
}

func recoverOwnedChild(child managedProcess) error {
	err := child.control("start")
	if !errors.Is(err, errPendingChild) {
		return err
	}
	if err := child.control("stop"); err != nil {
		return err
	}
	return child.control("start")
}

func (p applicationProfileRecord) checkBinding() error {
	secret, err := checkedFile(filepath.Join(applicationState, statefile.StateName), p.Agent.UID, 0600)
	if err != nil {
		return errApplication
	}
	defer secret.Close()
	raw, err := readBounded(secret, 128<<10)
	defer clear(raw)
	var binding struct {
		Version int
		State   struct {
			RouterID         string
			Origin           string
			InvitationDigest string
		}
	}
	if err != nil || json.Unmarshal(raw, &binding) != nil || binding.Version != 1 || binding.State.RouterID != p.RouterID ||
		binding.State.Origin != p.Agent.Origin || binding.State.InvitationDigest != "" {
		return errApplication
	}
	return nil
}

func ownsApplicationSocket(path string, pid int) bool {
	conn, err := net.DialTimeout("unix", path, 300*time.Millisecond)
	if err != nil {
		return false
	}
	defer conn.Close()
	socket, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return false
	}
	known := false
	err = socket.Control(func(fd uintptr) {
		peer, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		known = e == nil && peer.Uid == 0 && int(peer.Pid) == pid
	})
	return err == nil && known
}

const applicationInitScript = `#!/opt/bin/sh
set -eu
[ "$#" -ge 1 ] && [ "$#" -le 2 ] || exit 2
case "$1" in
  start|stop|status) action=$1 ;;
  check) action=status ;;
  *) exit 2 ;;
esac
exec /opt/libexec/xkeen-ui/agent-application/current/xkeen-entware-installer-linux-arm64 application-service "$action"
`
