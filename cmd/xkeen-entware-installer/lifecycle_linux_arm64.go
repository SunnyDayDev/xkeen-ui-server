package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const receiptPath = control + "/agent-transport.process.json"

var (
	errProcess      = errors.New("agent instance ownership is unavailable")
	errStopped      = errors.New("agent service is stopped")
	errPendingChild = errors.New("managed process startup is incomplete")
	errNotReady     = errors.New("installed agent did not confirm HTTPS authentication")
)

type receipt struct {
	Boot    string
	PID     int
	Tick    string
	Pending bool `json:",omitempty"`
}

func probePIDFD() error {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return unix.PidfdSendSignal(fd, 0, nil, 0)
}

func lifecycle(action string, p profile) error {
	if action != "stop" {
		if err := verifyProfileCA(p); err != nil {
			return err
		}
	}
	target, err := os.Readlink(current)
	if err != nil || target != "generations/"+p.Version {
		return errSafety
	}
	binary := filepath.Join(executableBase, target, agentAsset)
	if err := rootDirs(false, filepath.Dir(binary)); err != nil {
		return err
	}
	args := []string{"--server", p.Origin, "--state-dir", stateDir}
	if p.CADigest != "" {
		args = append(args, "--ca-file", profileDir+"/ca.pem")
	}
	return managedProcess{binary: binary, receipt: receiptPath, uid: p.UID, gid: p.GID, arguments: args, notReady: errNotReady}.control(action)
}

type managedProcess struct {
	binary          string
	receipt         string
	uid, gid        uint32
	arguments       []string
	notReady        error
	probe           func(int) bool
	readiness       func(string) bool
	useReadyPipe    bool
	noNewPrivileges bool
}

func (s managedProcess) control(action string) error {
	if err := rootDirs(false, filepath.Dir(s.binary)); err != nil {
		return err
	}
	file, err := checkedFile(s.binary, 0, 0755)
	if err != nil {
		return errSafety
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return errSafety
	}
	boot, err := currentBoot()
	if err != nil {
		return err
	}
	r, err := readReceipt(s.receipt)
	if err == nil {
		pidfd, active, err := identify(r, boot, s.uid, s.gid, info)
		if err != nil {
			return err
		}
		if active {
			defer unix.Close(pidfd)
			if action != "stop" {
				if r.Pending {
					return errPendingChild
				}
				if s.probe != nil && !s.probe(r.PID) {
					return s.notReady
				}
				return nil
			}
			if err := terminate(pidfd); err != nil {
				return err
			}
			return removePublished(s.receipt)
		}
		if err := removePublished(s.receipt); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	switch action {
	case "stop":
		return nil
	case "status":
		return errStopped
	default:
		return s.launch(boot, info)
	}
}

func readReceipt(path string) (receipt, error) {
	file, err := checkedFile(path, 0, 0600)
	if err != nil {
		return receipt{}, err
	}
	defer file.Close()
	var r receipt
	decoder := json.NewDecoder(io.LimitReader(file, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&r); err != nil {
		return receipt{}, errProcess
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return receipt{}, errProcess
	}
	return r, nil
}

func saveReceipt(path string, r receipt) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return errSafety
	}
	return writeFile(path, raw, 0600, 0, 0)
}

func processTick(pid int) (string, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	pos := strings.LastIndexByte(string(raw), ')')
	if pos < 0 {
		return "", errProcess
	}
	fields := strings.Fields(string(raw[pos+1:]))
	if len(fields) <= 19 {
		return "", errProcess
	}
	return fields[19], nil
}

func currentBoot() (string, error) {
	raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	boot := strings.TrimSpace(string(raw))
	if err != nil || len(boot) != 36 {
		return "", errProcess
	}
	return boot, nil
}

func identify(r receipt, boot string, uid, gid uint32, binary os.FileInfo) (int, bool, error) {
	if len(r.Boot) != 36 {
		return -1, false, errProcess
	}
	if r.Boot != boot {
		return -1, false, nil
	}
	if r.PID <= 1 || r.Tick == "" {
		return -1, false, errProcess
	}
	fd, err := unix.PidfdOpen(r.PID, 0)
	if errors.Is(err, unix.ESRCH) {
		return -1, false, nil
	}
	if err != nil {
		return -1, false, errProcess
	}
	ended, err := exited(fd, 0)
	if err != nil || ended {
		unix.Close(fd)
		return -1, false, err
	}
	tick, err := processTick(r.PID)
	if errors.Is(err, os.ErrNotExist) || (err == nil && tick != r.Tick) {
		unix.Close(fd)
		return -1, false, nil
	}
	principalErr := verifyProcessPrincipal(r.PID, uid, gid)
	exe, exeErr := os.Stat(fmt.Sprintf("/proc/%d/exe", r.PID))
	if err != nil || principalErr != nil || exeErr != nil || !os.SameFile(exe, binary) {
		unix.Close(fd)
		return -1, false, errProcess
	}
	return fd, true, nil
}

func verifyProcessPrincipal(pid int, uid, gid uint32) error {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return errProcess
	}
	for label, id := range map[string]uint32{"Uid": uid, "Gid": gid} {
		prefix := label + ":"
		expected := strconv.FormatUint(uint64(id), 10)
		matched := false
		for line := range strings.SplitSeq(string(raw), "\n") {
			fields, ok := strings.CutPrefix(line, prefix)
			if !ok {
				continue
			}
			values := strings.Fields(fields)
			if len(values) != 4 {
				return errProcess
			}
			for _, value := range values {
				if value != expected {
					return errProcess
				}
			}
			matched = true
			break
		}
		if !matched {
			return errProcess
		}
	}
	return nil
}

func exited(fd, timeout int) (bool, error) {
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
	for {
		n, err := unix.Poll(poll, timeout)
		if errors.Is(err, unix.EINTR) {
			timeout = max(0, int(time.Until(deadline)/time.Millisecond))
			continue
		}
		if err != nil || poll[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 {
			return false, errProcess
		}
		return n > 0 && poll[0].Revents&(unix.POLLIN|unix.POLLHUP) != 0, nil
	}
}

func terminate(fd int) error {
	if err := unix.PidfdSendSignal(fd, unix.SIGTERM, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
		return errProcess
	}
	dead, err := exited(fd, 7000)
	if err != nil || !dead {
		return errors.New("managed agent shutdown was not confirmed")
	}
	return nil
}

func (s managedProcess) launch(boot string, info os.FileInfo) error {
	cmd := exec.Command(s.binary, s.arguments...)
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/opt/bin:/opt/sbin:/usr/sbin:/usr/bin:/sbin:/bin", "SSL_CERT_FILE=/opt/etc/ssl/certs/ca-certificates.crt"}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:     true,
		Credential: &syscall.Credential{Uid: s.uid, Gid: s.gid, Groups: []uint32{}},
	}
	var reader, writer *os.File
	if s.probe == nil || s.useReadyPipe {
		var err error
		reader, writer, err = os.Pipe()
		if err != nil {
			return errProcess
		}
		defer reader.Close()
		defer writer.Close()
		if s.useReadyPipe {
			cmd.ExtraFiles = []*os.File{writer}
			cmd.Args = append(cmd.Args, "--ready-fd3")
		} else {
			cmd.Stdout = writer
		}
	}
	// The zero receipt forbids another start after an interrupted launch for
	// which an owned process has not yet been recorded.
	if err := saveReceipt(s.receipt, receipt{Boot: boot}); err != nil {
		return err
	}
	if err := s.startCommand(cmd); err != nil {
		_ = removePublished(s.receipt)
		return s.notReady
	}
	if writer != nil {
		writer.Close()
	}
	fail := func(err error) error {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if cleanupErr := removePublished(s.receipt); cleanupErr != nil {
			return cleanupErr
		}
		return err
	}
	tick, err := processTick(cmd.Process.Pid)
	if err != nil {
		return fail(s.notReady)
	}
	r := receipt{Boot: boot, PID: cmd.Process.Pid, Tick: tick, Pending: true}
	fd, active, err := identify(r, boot, s.uid, s.gid, info)
	if err != nil || !active {
		return fail(s.notReady)
	}
	defer unix.Close(fd)
	if err := saveReceipt(s.receipt, r); err != nil {
		return fail(err)
	}
	if (reader != nil && !s.waitMarker(reader)) || (s.probe != nil && !s.waitProbe(r.PID, fd)) {
		return fail(s.notReady)
	}
	if dead, err := exited(fd, 0); err != nil || dead {
		return fail(s.notReady)
	}
	r.Pending = false
	if err := saveReceipt(s.receipt, r); err != nil {
		return fail(err)
	}
	cmd.Process.Release()
	return nil
}

func (s managedProcess) startCommand(cmd *exec.Cmd) error {
	if !s.noNewPrivileges {
		return cmd.Start()
	}
	result := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
			result <- err
			return
		}
		result <- cmd.Start()
	}()
	return <-result
}

func (s managedProcess) waitProbe(pid, fd int) bool {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		dead, err := exited(fd, 50)
		if err != nil || dead {
			return false
		}
		if s.probe(pid) {
			return true
		}
	}
	return false
}

func (s managedProcess) waitMarker(reader io.Reader) bool {
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(io.LimitReader(reader, 4096))
		for scanner.Scan() {
			line := scanner.Text()
			if (s.readiness == nil && line == "agent authenticated") || (s.readiness != nil && s.readiness(line)) {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case ok := <-ready:
		return ok
	case <-timer.C:
		return false
	}
}
