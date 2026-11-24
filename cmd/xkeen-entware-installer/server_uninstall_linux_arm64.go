package main

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const serverRemovalPath = control + "/server.remove.json"
const heldServerInit = "#!/opt/bin/sh\nexit 1\n"

var errServerMaintenance = errors.New("server maintenance requires local recovery")

type serverRemoval struct {
	Release release
	Phase   string
	Owned   []release
}

func uninstallServer() error {
	lock, err := lockRole("server")
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := rootDirs(false, serverConfig, filepath.Dir(serverBase), filepath.Dir(serverInit)); err != nil {
		return err
	}
	p, err := loadServerProfile()
	if err != nil {
		return err
	}
	if _, err := os.Lstat(serverUpdatePath); !errors.Is(err, os.ErrNotExist) {
		return errServerMaintenance
	}
	record, err := loadServerRemoval(p)
	if err != nil {
		return err
	}
	init, initErr := readProtected(serverInit, 0755, 4096)
	if record.Phase == "" {
		if errors.Is(initErr, os.ErrNotExist) {
			if _, err := os.Lstat(serverBase); errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return errServerMaintenance
		}
		if initErr != nil || (string(init) != serverInitScript && string(init) != serverRecoveryInit && string(init) != heldServerInit) {
			return errServerMaintenance
		}
		if err := p.control("status"); err != nil && !errors.Is(err, errStopped) {
			return err
		}
		owned, err := knownServerReleases(p)
		if err != nil {
			return err
		}
		if err := cleanServerBinaries(p, owned, false); err != nil {
			return err
		}
		record = serverRemoval{Release: p.Release, Phase: "hold", Owned: owned}
		if err := saveServerRemoval(record); err != nil {
			return err
		}
	} else if initErr != nil && !(record.Phase == "remove" && errors.Is(initErr, os.ErrNotExist)) {
		return errServerMaintenance
	}
	if record.Phase == "hold" {
		if string(init) != serverInitScript && string(init) != serverRecoveryInit && string(init) != heldServerInit {
			return errServerMaintenance
		}
		target, err := os.Readlink(serverCurrent)
		if (err != nil && !errors.Is(err, os.ErrNotExist)) || (err == nil && target != "generations/"+p.Release.Version) {
			return errServerMaintenance
		}
		if removePublished(serverCurrent) != nil || writeFile(serverInit, []byte(heldServerInit), 0755, 0, 0) != nil {
			return errServerMaintenance
		}
		record.Phase = "stop"
		if err := saveServerRemoval(record); err != nil {
			return err
		}
	} else if initErr == nil && string(init) != heldServerInit {
		return errServerMaintenance
	}
	if record.Phase == "stop" {
		if err := p.controlBinary("stop", serverGenerationBinary(p)); err != nil {
			return err
		}
		record.Phase = "remove"
		if err := saveServerRemoval(record); err != nil {
			return err
		}
	}
	if err := cleanServerBinaries(p, record.Owned, true); err != nil {
		return err
	}
	if syncDir(filepath.Dir(serverBase)) != nil || removePublished(serverInit) != nil ||
		removePublished(serverRemovalPath) != nil {
		return errServerMaintenance
	}
	fmt.Println("Entware server removed; SQLite and TLS files retained")
	return nil
}

func loadServerRemoval(p serverProfile) (serverRemoval, error) {
	raw, err := readProtected(serverRemovalPath, 0600, 4096)
	if errors.Is(err, os.ErrNotExist) {
		return serverRemoval{}, nil
	}
	var record serverRemoval
	if err != nil || jsonv2.Unmarshal(raw, &record, jsonv2.RejectUnknownMembers(true)) != nil ||
		record.Release != p.Release || (record.Phase != "hold" && record.Phase != "stop" && record.Phase != "remove") {
		return record, errServerMaintenance
	}
	seen := map[string]bool{}
	for _, release := range record.Owned {
		if !validRelease(release) || seen[release.Version] {
			return record, errServerMaintenance
		}
		seen[release.Version] = true
	}
	if !seen[p.Release.Version] {
		return record, errServerMaintenance
	}
	return record, nil
}

func saveServerRemoval(record serverRemoval) error {
	raw, err := json.Marshal(record)
	if err != nil || writeFile(serverRemovalPath, raw, 0600, 0, 0) != nil {
		return errServerMaintenance
	}
	return nil
}

func cleanServerBinaries(p serverProfile, releases []release, remove bool) error {
	known := map[string]bool{}
	for _, release := range releases {
		known[release.Version] = true
	}
	generations := filepath.Join(serverBase, "generations")
	allowed := map[string]map[string]os.FileMode{
		serverBase:        {"current": os.ModeSymlink, "generations": os.ModeDir, "control": os.ModeDir},
		serverRecoveryDir: {installerAsset: 0755},
	}
	generationFiles := map[string]os.FileMode{installerAsset: 0755, serverAsset: 0755, "release.json": 0600}
	var entries, directories []string
	var inspect func(string) error
	inspect = func(dir string) error {
		items, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) && remove {
			return nil
		}
		if err != nil || rootDirs(false, dir) != nil {
			return errServerMaintenance
		}
		for _, item := range items {
			file := filepath.Join(dir, item.Name())
			mode, valid := allowed[dir][item.Name()]
			if dir == generations && known[item.Name()] {
				mode, valid = os.ModeDir, true
			}
			if filepath.Dir(dir) == generations {
				mode, valid = generationFiles[item.Name()]
			}
			if !valid {
				return errServerMaintenance
			}
			switch mode {
			case os.ModeDir:
				if err := inspect(file); err != nil {
					return err
				}
			case os.ModeSymlink:
				target, err := os.Readlink(file)
				if err != nil || target != "generations/"+p.Release.Version {
					return errServerMaintenance
				}
				entries = append(entries, file)
			default:
				checked, err := checkedFile(file, 0, mode)
				if err != nil {
					return errServerMaintenance
				}
				checked.Close()
				entries = append(entries, file)
			}
		}
		directories = append(directories, dir)
		return nil
	}
	if err := inspect(serverBase); err != nil || !remove {
		return err
	}
	for _, path := range append(entries, directories...) {
		if err := removePublished(path); err != nil {
			return err
		}
	}
	return nil
}

func knownServerReleases(p serverProfile) ([]release, error) {
	base := filepath.Join(serverBase, "generations")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, errServerMaintenance
	}
	var releases []release
	for _, entry := range entries {
		tag := entry.Name()
		if tag == p.Release.Version {
			releases = append(releases, p.Release)
			continue
		}
		dir := filepath.Join(base, tag)
		if len(tag) > 40 || !tagPattern.MatchString(tag) || rootDirs(false, dir) != nil {
			return nil, errServerMaintenance
		}
		raw, err := readProtected(filepath.Join(dir, "release.json"), 0600, 4096)
		var proof release
		if err != nil || jsonv2.Unmarshal(raw, &proof, jsonv2.RejectUnknownMembers(true)) != nil || !validRelease(proof) || proof.Version != tag {
			return nil, errServerMaintenance
		}
		releases = append(releases, proof)
	}
	return releases, nil
}
