package main

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const serverUpdatePath = control + "/server.update.json"
const serverRecoveryDir = serverBase + "/control"
const serverRecoveryBinary = serverRecoveryDir + "/" + installerAsset

var serverRecoveryInit = strings.Replace(serverInitScript, "/current/", "/control/", 1)
var maintenanceFormat = regexp.MustCompile(`^central-sqlite-v[1-9][0-9]*/[1-9][0-9]*\n$`)

// Overridden only by the synthetic process harness.
var afterServerPreflight = func() error { return nil }
var afterServerAdmission = func() error { return nil }

type serverUpdate struct {
	Before, Next serverProfile
	Phase        string
	WasRunning   *bool
	OldFormat    string
	Backup       string
}

func updateServer(stage, version, manifest string) error {
	selected := release{Version: version, Manifest: manifest}
	if !trustedStage(stage) || !validRelease(selected) || probePIDFD() != nil {
		return errServerMaintenance
	}
	lock, err := lockRole("server")
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := rootDirs(false, serverConfig, serverBase, filepath.Dir(serverInit)); err != nil {
		return err
	}
	if _, err := os.Lstat(serverRemovalPath); !errors.Is(err, os.ErrNotExist) {
		return errServerMaintenance
	}
	before, err := loadServerProfile()
	if err != nil {
		return err
	}
	if err := recoverServerUpdate(before); err != nil {
		return err
	}
	before, err = loadServerProfile()
	if err != nil {
		return err
	}
	if before.Release.Version == selected.Version {
		if before.Release != selected {
			return errServerMaintenance
		}
		fmt.Println("Entware server release is already installed")
		return nil
	}
	init, err := readProtected(serverInit, 0755, 4096)
	if err != nil || (string(init) != serverInitScript && string(init) != serverRecoveryInit) {
		return errServerMaintenance
	}
	if _, err := before.installedCertificate(); err != nil {
		return err
	}
	if _, err := before.Validator.arguments(before.GID); err != nil {
		return err
	}
	if err := bindServerRelease(before); err != nil {
		return err
	}
	next := before
	next.Release = selected
	if err := prepareServerSuccessor(stage, next); err != nil {
		return err
	}
	scratch, err := serverMaintenanceDirectory(before, ".preflight-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	if _, err := serverStorage(next, "restore", "--backup", serverDB, "--output", filepath.Join(scratch, "probe.sqlite")); err != nil {
		return errors.New("server release rejected the preflight database")
	}
	if err := afterServerPreflight(); err != nil {
		return err
	}
	running, err := serverIsRunning(before)
	if err != nil {
		return err
	}
	if err := installServerRecoveryLauncher(stage); err != nil {
		return err
	}
	intent := serverUpdate{Before: before, Next: next, Phase: "prepared", WasRunning: &running}
	if err := saveServerUpdate(intent); err != nil {
		return err
	}
	if err := before.control("stop"); err != nil {
		return err
	}
	backupDir, err := serverMaintenanceDirectory(before, "backup-")
	if err != nil {
		return serverUpdateRejected(before, err)
	}
	backup := filepath.Join(backupDir, "server.sqlite")
	if _, err := serverStorage(next, "backup", "--db-path", serverDB, "--output", backup); err != nil {
		return serverUpdateRejected(before, errors.New("server update could not protect final state"))
	}
	if _, err := serverStorage(next, "restore", "--backup", backup, "--output", filepath.Join(scratch, "final.sqlite")); err != nil {
		return serverUpdateRejected(before, errors.New("server release rejected the final database"))
	}
	intent.Backup = filepath.Base(backupDir)
	intent.OldFormat, _ = serverStorage(before, "compatibility", "--db-path", serverDB)
	intent.Phase = "admitted"
	if err := saveServerUpdate(intent); err != nil {
		return err
	}
	if err := selectServerGeneration(next); err != nil {
		return errServerMaintenance
	}
	if err := afterServerAdmission(); err != nil {
		return serverUpdateRejected(next, err)
	}
	if running {
		if err := next.control("start"); err != nil {
			return serverUpdateRejected(next, errors.New("new server did not confirm readiness"))
		}
	} else if _, err := serverStorage(next, "compatibility", "--db-path", serverDB); err != nil {
		// Avoid starting a service that the owner deliberately stopped. Its
		// first ordinary startup will perform the rehearsed migration.
		if intent.OldFormat == "" {
			return errServerMaintenance
		}
	}
	intent.Phase = "committed"
	if err := saveServerUpdate(intent); err != nil {
		return err
	}
	if err := removePublished(serverUpdatePath); err != nil {
		return err
	}
	if running {
		fmt.Println("Entware server update ready")
	} else {
		fmt.Println("Entware server update selected; service remains stopped")
	}
	return nil
}

func serverMaintenanceDirectory(p serverProfile, prefix string) (string, error) {
	if err := prepareServerState(p); err != nil {
		return "", err
	}
	// Change ownership while the directory entry is still under a parent
	// controlled by root. The service UID owns serverState.
	if err := rootDirs(false, stateBase); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(stateBase, prefix)
	if err != nil {
		return "", errServerMaintenance
	}
	defer os.Remove(stage)
	dir, err := os.Open(stage)
	if err != nil {
		return "", errServerMaintenance
	}
	defer dir.Close()
	if dir.Chown(int(p.UID), int(p.GID)) != nil || dir.Sync() != nil {
		return "", errServerMaintenance
	}
	path := filepath.Join(serverState, filepath.Base(stage))
	if unix.Renameat2(unix.AT_FDCWD, stage, unix.AT_FDCWD, path, unix.RENAME_NOREPLACE) != nil ||
		syncDir(stateBase) != nil || syncDir(serverState) != nil {
		return "", errServerMaintenance
	}
	return path, nil
}

func serverStorage(p serverProfile, args ...string) (string, error) {
	binary := serverGenerationBinary(p)
	if err := rootDirs(false, filepath.Dir(binary)); err != nil {
		return "", errServerMaintenance
	}
	file, err := checkedFile(binary, 0, 0755)
	if err != nil {
		return "", errServerMaintenance
	}
	defer file.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3", append([]string{"storage"}, args...)...)
	cmd.ExtraFiles = []*os.File{file}
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/opt/bin:/opt/sbin:/usr/sbin:/usr/bin:/sbin:/bin"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: p.UID, Gid: p.GID, Groups: []uint32{}}}
	raw, err := cmd.Output()
	if err != nil || len(raw) > 1024 {
		return "", errServerMaintenance
	}
	return string(raw), nil
}

func serverGenerationBinary(p serverProfile) string {
	return filepath.Join(serverBase, "generations", p.Release.Version, serverAsset)
}

func bindServerRelease(p serverProfile) error {
	meta := filepath.Join(filepath.Dir(serverGenerationBinary(p)), "release.json")
	if raw, err := readProtected(meta, 0600, 4096); err == nil {
		var stored release
		if jsonv2.Unmarshal(raw, &stored, jsonv2.RejectUnknownMembers(true)) != nil || stored != p.Release {
			return errServerMaintenance
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return errServerMaintenance
	}
	raw, err := json.Marshal(p.Release)
	if err != nil {
		return errServerMaintenance
	}
	return writeFile(meta, raw, 0600, 0, 0)
}

func prepareServerSuccessor(stage string, next serverProfile) error {
	target := "generations/" + next.Release.Version
	meta := filepath.Join(serverBase, target, "release.json")
	if raw, err := readProtected(meta, 0600, 4096); err == nil {
		var stored release
		if jsonv2.Unmarshal(raw, &stored, jsonv2.RejectUnknownMembers(true)) != nil || stored != next.Release {
			return errServerMaintenance
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return errServerMaintenance
	}
	if err := prepareServerGeneration(stage, target); err != nil {
		return err
	}
	raw, err := json.Marshal(next.Release)
	if err != nil {
		return errServerMaintenance
	}
	return writeFile(meta, raw, 0600, 0, 0)
}

func installServerRecoveryLauncher(stage string) error {
	if err := rootDirs(true, serverRecoveryDir); err != nil {
		return err
	}
	raw, err := readProtected(filepath.Join(stage, installerAsset), 0700, 64<<20)
	if err != nil {
		return errServerMaintenance
	}
	if err := writeFile(serverRecoveryBinary, raw, 0755, 0, 0); err != nil {
		return err
	}
	return writeFile(serverInit, []byte(serverRecoveryInit), 0755, 0, 0)
}

func selectServerGeneration(p serverProfile) error {
	expected := filepath.Join("generations", p.Release.Version)
	if err := rootDirs(false, filepath.Join(serverBase, expected)); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(serverBase, ".current-")
	if err != nil {
		return errServerMaintenance
	}
	defer os.RemoveAll(stage)
	link := filepath.Join(stage, "current")
	if os.Symlink(expected, link) != nil || os.Rename(link, serverCurrent) != nil || syncDir(serverBase) != nil {
		return errServerMaintenance
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return errServerMaintenance
	}
	return writeFile(serverProfilePath, raw, 0600, 0, 0)
}

func serverIsRunning(p serverProfile) (bool, error) {
	err := p.control("status")
	if errors.Is(err, errStopped) {
		return false, nil
	}
	return err == nil, err
}

func saveServerUpdate(intent serverUpdate) error {
	raw, err := json.Marshal(intent)
	if err != nil {
		return errServerMaintenance
	}
	return writeFile(serverUpdatePath, raw, 0600, 0, 0)
}

func recoverServerUpdate(actual serverProfile) error {
	raw, err := readProtected(serverUpdatePath, 0600, 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var record serverUpdate
	if err != nil || jsonv2.Unmarshal(raw, &record, jsonv2.RejectUnknownMembers(true)) != nil || record.WasRunning == nil ||
		!validRelease(record.Before.Release) || !validRelease(record.Next.Release) || record.Before.Release.Version == record.Next.Release.Version ||
		(actual != record.Before && actual != record.Next) {
		return errServerMaintenance
	}
	bound := record.Before
	bound.Release = record.Next.Release
	if bound != record.Next {
		return errServerMaintenance
	}
	target, err := os.Readlink(serverCurrent)
	if err != nil || (target != "generations/"+record.Before.Release.Version && target != "generations/"+record.Next.Release.Version) {
		return errServerMaintenance
	}
	if syncDir(serverBase) != nil || syncDir(serverConfig) != nil {
		return errServerMaintenance
	}
	if record.Phase != "prepared" {
		if matched, _ := filepath.Match("backup-*", record.Backup); !matched || filepath.Base(record.Backup) != record.Backup {
			return errServerMaintenance
		}
	}
	switch record.Phase {
	case "committed":
		if target != "generations/"+record.Next.Release.Version || actual != record.Next {
			return errServerMaintenance
		}
		return removePublished(serverUpdatePath)
	case "admitted":
		if err := record.Next.controlBinary("stop", serverGenerationBinary(record.Next)); err != nil {
			return err
		}
		if err := verifyRollbackCompatibility(record); err != nil {
			return err
		}
		record.Phase = "restoring"
		if err := saveServerUpdate(record); err != nil {
			return err
		}
		fallthrough
	case "restoring":
		if err := verifyRollbackCompatibility(record); err != nil {
			return err
		}
		if target != "generations/"+record.Before.Release.Version || actual != record.Before {
			if err := selectServerGeneration(record.Before); err != nil {
				return err
			}
		}
	case "prepared":
		if target != "generations/"+record.Before.Release.Version || actual != record.Before {
			return errServerMaintenance
		}
	default:
		return errServerMaintenance
	}
	if *record.WasRunning {
		if err := record.Before.control("start"); err != nil {
			return err
		}
	}
	return removePublished(serverUpdatePath)
}

func verifyRollbackCompatibility(record serverUpdate) error {
	old, err := serverStorage(record.Before, "compatibility", "--db-path", serverDB)
	if err != nil || old != record.OldFormat || !maintenanceFormat.MatchString(old) {
		return errServerMaintenance
	}
	next, err := serverStorage(record.Next, "compatibility", "--db-path", serverDB)
	if err != nil || next != old {
		return errServerMaintenance
	}
	return nil
}

func serverUpdateRejected(actual serverProfile, cause error) error {
	if err := recoverServerUpdate(actual); err != nil {
		return err
	}
	return fmt.Errorf("%w; previous server generation restored", cause)
}
