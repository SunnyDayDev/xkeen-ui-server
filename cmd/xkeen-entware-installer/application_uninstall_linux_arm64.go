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
	"time"
)

const applicationRemovalPath = control + "/agent-application.remove.json"
const heldApplicationInit = "#!/opt/bin/sh\nexit 1\n"

type applicationRemoval struct {
	Profile applicationProfileRecord
	Phase   string
}

func uninstallApplication() error {
	p, err := loadApplicationProfile()
	if err != nil {
		return errApplication
	}
	admission, err := lockAdmission("agent-application", p.Agent.UID, p.Agent.GID)
	if err != nil {
		return err
	}
	defer admission.Close()
	lock, err := lockRole("agent-application")
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := rootDirs(false, applicationConfigDir, filepath.Dir(applicationBase), filepath.Dir(applicationInit)); err != nil {
		return errApplication
	}
	if actual, err := loadApplicationProfile(); err != nil || actual != p {
		return errApplication
	}
	record, err := loadApplicationRemoval(p)
	if err != nil {
		return err
	}
	init, initErr := readProtected(applicationInit, 0755, 4096)
	if record.Phase == "" {
		if errors.Is(initErr, os.ErrNotExist) {
			if _, err := os.Lstat(applicationBase); errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return errApplication
		}
		if initErr != nil || (string(init) != applicationInitScript && string(init) != heldApplicationInit) {
			return errApplication
		}
		if err := p.verifySources(); err != nil {
			return err
		}
		if err := cleanAgentBinaries(applicationBase, p.Agent, false); err != nil {
			return err
		}
		if err := writeFile(applicationInit, []byte(heldApplicationInit), 0755, 0, 0); err != nil {
			return errApplication
		}
		record = applicationRemoval{Profile: p, Phase: "stop"}
		if err := saveApplicationRemoval(record); err != nil {
			return err
		}
	} else if initErr != nil && !(record.Phase == "remove" && errors.Is(initErr, os.ErrNotExist)) {
		return errApplication
	} else if initErr == nil && string(init) != heldApplicationInit {
		return errApplication
	}
	if record.Phase == "stop" {
		if err := p.verifySources(); err != nil {
			return err
		}
		if err := p.control("stop", false); err != nil {
			return err
		}
		if err := p.detachRuntime(); err != nil {
			return err
		}
		record.Phase = "remove"
		if err := saveApplicationRemoval(record); err != nil {
			return err
		}
	}
	stateLock, err := lockRetainedAgentState(applicationState, p.Agent)
	if err != nil {
		return err
	}
	defer stateLock.Close()
	if err := cleanAgentBinaries(applicationBase, p.Agent, true); err != nil {
		return err
	}
	if syncDir(filepath.Dir(applicationBase)) != nil || removePublished(applicationInit) != nil ||
		removePublished(applicationRemovalPath) != nil {
		return errApplication
	}
	fmt.Println("Entware managed agent removed; runtime stopped; former startup entrypoint not verified")
	return nil
}

func (p applicationProfileRecord) detachRuntime() error {
	dir := filepath.Join(applicationBase, "generations", p.Agent.Version)
	if err := rootDirs(false, dir); err != nil {
		return err
	}
	binary, err := checkedFile(filepath.Join(dir, supervisorAsset), 0, 0755)
	if err != nil {
		return err
	}
	defer binary.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3", "--config", p.LocalPath,
		"--application-config", p.ApplicationPath, "--detach-runtime")
	cmd.ExtraFiles = []*os.File{binary}
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/opt/bin:/opt/sbin:/usr/sbin:/usr/bin:/sbin:/bin"}
	if err := (managedProcess{noNewPrivileges: true}).startCommand(cmd); err != nil {
		return errApplication
	}
	if err := cmd.Wait(); err != nil {
		return errors.New("managed runtime or journal needs local recovery; startup is held")
	}
	return nil
}

func loadApplicationRemoval(p applicationProfileRecord) (applicationRemoval, error) {
	raw, err := readProtected(applicationRemovalPath, 0600, 16<<10)
	if errors.Is(err, os.ErrNotExist) {
		return applicationRemoval{}, nil
	}
	var record applicationRemoval
	if err != nil || jsonv2.Unmarshal(raw, &record, jsonv2.RejectUnknownMembers(true)) != nil ||
		record.Profile != p || (record.Phase != "stop" && record.Phase != "remove") {
		return record, errApplication
	}
	return record, nil
}

func saveApplicationRemoval(record applicationRemoval) error {
	raw, err := json.Marshal(record)
	if err != nil || writeFile(applicationRemovalPath, raw, 0600, 0, 0) != nil {
		return errApplication
	}
	return nil
}
