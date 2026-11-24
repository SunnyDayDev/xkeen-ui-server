package main

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	updatePath       = control + "/agent-transport.update.json"
	recoveryDir      = executableBase + "/control"
	recoveryBinary   = recoveryDir + "/" + installerAsset
	formatAsset      = "agent-state-format.txt"
	compatibleFormat = "agent-transport-state-v1\n"
)

var (
	errRecovery        = errors.New("Entware update requires local recovery")
	recoveryInitScript = strings.Replace(initScript, "/current/", "/control/", 1)
)

var syncCurrent = func() error { return syncDir(executableBase) }

type release struct {
	Version  string
	Manifest string
}

type updateIntent struct {
	Before     profile
	Next       profile
	WasRunning bool
	Committed  bool
}

func update(stage, version, manifest string) error {
	if !trustedStage(stage) || !validRelease(release{version, manifest}) {
		return errUsage
	}
	lock, err := lockService()
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := rootDirs(false, profileDir, executableBase, executableBase+"/generations", filepath.Dir(initPath)); err != nil {
		return err
	}
	base, err := readProfile()
	if err != nil {
		return err
	}
	if err := verifyProfileCA(base); err != nil {
		return err
	}
	init, err := readProtected(initPath, 0755, 4096)
	if err != nil || (string(init) != initScript && string(init) != recoveryInitScript) {
		return errors.New("agent init registration is unavailable or foreign")
	}
	if err := recoverUpdate(base); err != nil {
		return err
	}
	before, err := activeProfile(base)
	if err != nil {
		return err
	}
	if before.Version == version {
		if before.Manifest != manifest {
			return errors.New("selected tag is already bound to another manifest")
		}
		fmt.Println("Entware agent release is already installed")
		return nil
	}
	format, err := readProtected(filepath.Join(stage, formatAsset), 0600, 256)
	if err != nil || string(format) != compatibleFormat {
		return errors.New("release does not declare compatible agent state")
	}
	next := base
	next.Version, next.Manifest = version, manifest
	if err := stageUpdateRelease(stage, base, next); err != nil {
		return err
	}
	if err := installRecoveryLauncher(stage, string(init)); err != nil {
		return err
	}
	running, err := serviceRunning(before)
	if err != nil {
		return err
	}
	intent := updateIntent{Before: before, Next: next, WasRunning: running}
	if err := saveUpdate(intent); err != nil {
		return errRecovery
	}
	if err := lifecycle("stop", before); err != nil {
		return errRecovery
	}
	if err := pointTo(next); err != nil {
		return errRecovery
	}
	if err := lifecycle("start", next); err != nil {
		return rollbackUpdate(base)
	}
	if !intent.WasRunning {
		if err := lifecycle("stop", next); err != nil {
			return errRecovery
		}
	}
	intent.Committed = true
	if err := saveUpdate(intent); err != nil {
		return errRecovery
	}
	if err := removePublished(updatePath); err != nil {
		return errRecovery
	}
	fmt.Println("Entware agent update ready")
	return nil
}

func installedLifecycle(action string, base profile) error {
	switch action {
	case "start":
		if err := recoverUpdate(base); err != nil {
			return err
		}
	default:
		if _, err := os.Lstat(updatePath); !errors.Is(err, os.ErrNotExist) {
			return errRecovery
		}
	}
	p, err := activeProfile(base)
	if err != nil {
		return err
	}
	return lifecycle(action, p)
}

func serviceRunning(p profile) (bool, error) {
	err := lifecycle("status", p)
	if errors.Is(err, errStopped) {
		return false, nil
	}
	return err == nil, err
}

func installRecoveryLauncher(stage, priorInit string) error {
	if err := rootDirs(true, recoveryDir); err != nil {
		return err
	}
	if priorInit == recoveryInitScript {
		bin, err := checkedFile(recoveryBinary, 0, 0755)
		if err != nil {
			return errRecovery
		}
		bin.Close()
		return nil
	}
	raw, err := readProtected(filepath.Join(stage, installerAsset), 0700, 64<<20)
	if err != nil {
		return errSafety
	}
	if err := writeFile(recoveryBinary, raw, 0755, 0, 0); err != nil {
		return err
	}
	return writeFile(initPath, []byte(recoveryInitScript), 0755, 0, 0)
}

func stageUpdateRelease(stage string, base, next profile) error {
	stored, err := readRelease(base, next.Version)
	switch {
	case err == nil && stored.Manifest == next.Manifest:
		return nil
	case err == nil || !errors.Is(err, os.ErrNotExist):
		return errors.New("candidate generation is unavailable or associated with another manifest")
	}
	if err := prepareGeneration(stage, next); err != nil {
		return err
	}
	raw, err := json.Marshal(release{next.Version, next.Manifest})
	if err != nil {
		return errSafety
	}
	return writeFile(filepath.Join(executableBase, "generations", next.Version, "release.json"), raw, 0600, 0, 0)
}

func validRelease(r release) bool {
	return len(r.Version) <= 40 && tagPattern.MatchString(r.Version) && shaPattern.MatchString(r.Manifest)
}

func readRelease(base profile, version string) (release, error) {
	if len(version) > 40 || !tagPattern.MatchString(version) {
		return release{}, errSafety
	}
	if version == base.Version {
		return release{base.Version, base.Manifest}, nil
	}
	dir := filepath.Join(executableBase, "generations", version)
	if _, err := os.Lstat(dir); err != nil {
		return release{}, err
	}
	if err := rootDirs(false, dir); err != nil {
		return release{}, err
	}
	raw, err := readProtected(filepath.Join(dir, "release.json"), 0600, 4096)
	if err != nil {
		return release{}, err
	}
	var r release
	if jsonv2.Unmarshal(raw, &r, jsonv2.RejectUnknownMembers(true)) != nil || !validRelease(r) || r.Version != version {
		return release{}, errSafety
	}
	return r, nil
}

func activeProfile(base profile) (profile, error) {
	target, err := os.Readlink(current)
	if err != nil {
		return profile{}, errSafety
	}
	version, ok := strings.CutPrefix(target, "generations/")
	if !ok {
		return profile{}, errSafety
	}
	r, err := readRelease(base, version)
	if err != nil {
		return profile{}, errSafety
	}
	base.Version, base.Manifest = r.Version, r.Manifest
	return base, nil
}

func pointTo(p profile) error {
	temp, err := os.MkdirTemp(executableBase, ".current-")
	if err != nil {
		return errSafety
	}
	defer os.RemoveAll(temp)
	link := filepath.Join(temp, "current")
	if os.Symlink("generations/"+p.Version, link) != nil || os.Rename(link, current) != nil || syncCurrent() != nil {
		return errSafety
	}
	return nil
}

func saveUpdate(intent updateIntent) error {
	raw, err := json.Marshal(intent)
	if err != nil {
		return errRecovery
	}
	return writeFile(updatePath, raw, 0600, 0, 0)
}

func loadUpdate(base profile) (updateIntent, bool, error) {
	raw, err := readProtected(updatePath, 0600, 16384)
	if errors.Is(err, os.ErrNotExist) {
		return updateIntent{}, false, nil
	}
	if err != nil || syncDir(control) != nil {
		return updateIntent{}, false, errRecovery
	}
	var stored struct {
		Before     profile
		Next       profile
		WasRunning *bool
		Committed  *bool
	}
	if jsonv2.Unmarshal(raw, &stored, jsonv2.RejectUnknownMembers(true)) != nil ||
		stored.Before.Version == stored.Next.Version || stored.WasRunning == nil || stored.Committed == nil {
		return updateIntent{}, false, errRecovery
	}
	intent := updateIntent{Before: stored.Before, Next: stored.Next, WasRunning: *stored.WasRunning, Committed: *stored.Committed}

	for _, p := range []profile{intent.Before, intent.Next} {
		r, err := readRelease(base, p.Version)
		expected := base
		expected.Version, expected.Manifest = r.Version, r.Manifest
		if err != nil || expected != p {
			return updateIntent{}, false, errRecovery
		}
	}
	return intent, true, nil
}

func rollbackUpdate(base profile) error {
	if err := recoverUpdate(base); err != nil {
		return err
	}
	return errors.New("new agent did not become ready; previous generation restored")
}

func recoverUpdate(base profile) error {
	intent, ok, err := loadUpdate(base)
	if err != nil || !ok {
		return err
	}
	active, err := activeProfile(base)
	if err != nil || (active != intent.Before && active != intent.Next) {
		return errRecovery
	}
	// A previous attempt can have renamed current and failed to persist the
	// parent directory. Qualify the observed link durably before any restart
	// or removal of the only recovery intent.
	if syncCurrent() != nil {
		return errRecovery
	}

	if intent.Committed {
		if active != intent.Next {
			return errRecovery
		}
		return removePublished(updatePath)
	}
	if active == intent.Next {
		if lifecycle("stop", active) != nil || pointTo(intent.Before) != nil {
			return errRecovery
		}
	}
	if intent.WasRunning {
		if lifecycle("start", intent.Before) != nil {
			return errRecovery
		}
	} else if lifecycle("stop", intent.Before) != nil {
		return errRecovery
	}
	return removePublished(updatePath)
}

func readProtected(path string, mode os.FileMode, maximum int64) ([]byte, error) {
	file, err := checkedFile(path, 0, mode)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) > maximum {
		return nil, errSafety
	}
	return raw, nil
}
