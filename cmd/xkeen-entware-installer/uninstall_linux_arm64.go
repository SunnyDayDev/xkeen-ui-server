package main

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

const transportRemovalPath = control + "/agent-transport.remove.json"
const heldTransportInit = "#!/opt/bin/sh\nexit 1\n"

var errRemoval = errors.New("agent service removal requires local recovery")

type transportRemoval struct {
	Active profile
	Phase  string
}

func uninstallTransport() error {
	base, err := readProfile()
	if err != nil {
		return errRemoval
	}
	admission, err := lockAdmission("agent-transport", base.UID, base.GID)
	if err != nil {
		return err
	}
	defer admission.Close()
	lock, err := lockService()
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := rootDirs(false, profileDir, filepath.Dir(executableBase), filepath.Dir(initPath)); err != nil {
		return err
	}
	currentBase, err := readProfile()
	if err != nil || currentBase != base {
		return errRemoval
	}
	record, err := loadTransportRemoval(base)
	if err != nil {
		return err
	}
	if record.Phase == "" {
		initial, err := readProtected(initPath, 0755, 4096)
		if errors.Is(err, os.ErrNotExist) {
			if _, err := os.Lstat(executableBase); errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return errRemoval
		}
		if err != nil || (string(initial) != initScript && string(initial) != recoveryInitScript && string(initial) != heldTransportInit) {
			return errRemoval
		}
		if err := recoverUpdate(base); err != nil {
			return err
		}
		active, err := activeProfile(base)
		if err != nil {
			return err
		}
		if _, err := serviceRunning(active); err != nil {
			return err
		}
		if err := cleanTransportFiles(active, false); err != nil {
			return err
		}
		if err := writeFile(initPath, []byte(heldTransportInit), 0755, 0, 0); err != nil {
			return errRemoval
		}
		record = transportRemoval{Active: active, Phase: "stop"}
		if err := saveTransportRemoval(record); err != nil {
			return err
		}
	}
	init, err := readProtected(initPath, 0755, 4096)
	if err != nil && !(record.Phase == "remove" && errors.Is(err, os.ErrNotExist)) {
		return errRemoval
	}
	if err == nil && string(init) != heldTransportInit {
		return errRemoval
	}
	if record.Phase == "stop" {
		if err := lifecycle("stop", record.Active); err != nil {
			return err
		}
		raw, err := json.Marshal(record.Active)
		if err != nil || writeFile(profilePath, raw, 0600, 0, 0) != nil {
			return errRemoval
		}
		record.Phase = "remove"
		if err := saveTransportRemoval(record); err != nil {
			return err
		}
	}
	stateLock, err := lockRetainedTransportState(record.Active)
	if err != nil {
		return err
	}
	defer stateLock.Close()
	if err := cleanTransportFiles(record.Active, true); err != nil {
		return err
	}
	if err := syncDir(filepath.Dir(executableBase)); err != nil {
		return errRemoval
	}
	if err := removePublished(initPath); err != nil {
		return err
	}
	if err := removePublished(transportRemovalPath); err != nil {
		return err
	}
	fmt.Println("Entware transport agent removed; private state retained")
	return nil
}

func loadTransportRemoval(base profile) (transportRemoval, error) {
	raw, err := readProtected(transportRemovalPath, 0600, 4096)
	if errors.Is(err, os.ErrNotExist) {
		return transportRemoval{}, nil
	}
	var record transportRemoval
	if err != nil || jsonv2.Unmarshal(raw, &record, jsonv2.RejectUnknownMembers(true)) != nil ||
		(record.Phase != "stop" && record.Phase != "remove") ||
		!validRelease(release{record.Active.Version, record.Active.Manifest}) {
		return record, errRemoval
	}
	retained := record.Active
	retained.Version, retained.Manifest = base.Version, base.Manifest
	if retained != base || (record.Phase == "remove" && record.Active != base) {
		return record, errRemoval
	}
	return record, nil
}

func saveTransportRemoval(record transportRemoval) error {
	raw, err := json.Marshal(record)
	if err != nil || writeFile(transportRemovalPath, raw, 0600, 0, 0) != nil {
		return errRemoval
	}
	return nil
}

func cleanTransportFiles(active profile, remove bool) error {
	// Inventory first, so an unexpected file blocks the operation before any
	// deletion. No recursive removal is used, including on interrupted retries.
	var files, dirs []string
	var inventory func(string) error
	inventory = func(path string) error {
		entries, err := os.ReadDir(path)
		if errors.Is(err, os.ErrNotExist) && remove {
			return nil
		}
		if err != nil || rootDirs(false, path) != nil {
			return errRemoval
		}
		for _, entry := range entries {
			name := entry.Name()
			child := filepath.Join(path, name)
			if path == executableBase && name == "current" {
				target, err := os.Readlink(child)
				if err != nil || target != "generations/"+active.Version {
					return errRemoval
				}
				files = append(files, child)
				continue
			}
			if (path == executableBase && (name == "generations" || name == "control")) ||
				(path == filepath.Join(executableBase, "generations") && tagPattern.MatchString(name) && len(name) <= 40) {
				if err := inventory(child); err != nil {
					return err
				}
				continue
			}
			mode := os.FileMode(0755)
			generation := filepath.Dir(path) == filepath.Join(executableBase, "generations")
			switch {
			case (generation || path == recoveryDir) && name == installerAsset:
			case generation && name == agentAsset:
			case generation && name == "release.json":
				mode = 0600
			default:
				return errRemoval
			}
			file, err := checkedFile(child, 0, mode)
			if err != nil {
				return errRemoval
			}
			file.Close()
			files = append(files, child)
		}
		dirs = append(dirs, path)
		return nil
	}
	if err := inventory(executableBase); err != nil || !remove {
		return err
	}
	for _, path := range append(files, dirs...) {
		if err := removePublished(path); err != nil {
			return err
		}
	}
	return nil
}

func lockRetainedTransportState(p profile) (*os.File, error) {
	info, err := os.Lstat(stateDir)
	if err != nil || !owned(info, p.UID, os.ModeDir, 0700) || info.Sys().(*syscall.Stat_t).Gid != p.GID {
		return nil, errRemoval
	}
	lock, err := checkedFile(filepath.Join(stateDir, ".lock"), p.UID, 0600)
	if err != nil {
		return nil, errRemoval
	}
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		lock.Close()
		return nil, errProcess
	}
	return lock, nil
}
