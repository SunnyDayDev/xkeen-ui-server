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
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const receiptPath = control + "/agent-transport.process.json"

var (
	errProcess  = errors.New("agent instance ownership is unavailable")
	errStopped  = errors.New("agent service is stopped")
	errNotReady = errors.New("installed agent did not confirm HTTPS authentication")
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
	file, err := checkedFile(binary, 0, 0755)
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
	r, err := readReceipt()
	if err == nil {
		pidfd, active, err := identify(r, boot, p, info)
		if err != nil {
			return err
		}
		if active {
			defer unix.Close(pidfd)
			if action != "stop" {
				if r.Pending {
					return errProcess
				}
				return nil
			}
			if err := terminate(pidfd); err != nil {
				return err
			}
			return removePublished(receiptPath)
		}
		if err := removePublished(receiptPath); err != nil {
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
		return launch(binary, boot, p, info)
	}
}

func readReceipt() (receipt, error) {
	file, err := checkedFile(receiptPath, 0, 0600)
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

func saveReceipt(r receipt) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return errSafety
	}
	return writeFile(receiptPath, raw, 0600, 0, 0)
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

func identify(r receipt, boot string, p profile, binary os.FileInfo) (int, bool, error) {
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
	principalErr := verifyProcessPrincipal(r.PID, p)
	exe, exeErr := os.Stat(fmt.Sprintf("/proc/%d/exe", r.PID))
	if err != nil || principalErr != nil || exeErr != nil || !os.SameFile(exe, binary) {
		unix.Close(fd)
		return -1, false, errProcess
	}
	return fd, true, nil
}

func verifyProcessPrincipal(pid int, p profile) error {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return errProcess
	}
	for label, id := range map[string]uint32{"Uid": p.UID, "Gid": p.GID} {
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

func launch(binary, boot string, p profile, info os.FileInfo) error {
	read, write, err := os.Pipe()
	if err != nil {
		return errProcess
	}
	defer read.Close()
	defer write.Close()
	args := []string{"--server", p.Origin, "--state-dir", stateDir}
	if p.CADigest != "" {
		args = append(args, "--ca-file", profileDir+"/ca.pem")
	}
	cmd := exec.Command(binary, args...)
	cmd.Stdout = write
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/opt/bin:/opt/sbin:/usr/sbin:/usr/bin:/sbin:/bin", "SSL_CERT_FILE=/opt/etc/ssl/certs/ca-certificates.crt"}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:     true,
		Credential: &syscall.Credential{Uid: p.UID, Gid: p.GID, Groups: []uint32{}},
	}
	// The zero receipt forbids another start after an interrupted launch for
	// which an owned process has not yet been recorded.
	if err := saveReceipt(receipt{Boot: boot}); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = removePublished(receiptPath)
		return errProcess
	}
	write.Close()
	fail := func(err error) error {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if cleanupErr := removePublished(receiptPath); cleanupErr != nil {
			return cleanupErr
		}
		return err
	}
	tick, err := processTick(cmd.Process.Pid)
	if err != nil {
		return fail(errProcess)
	}
	r := receipt{Boot: boot, PID: cmd.Process.Pid, Tick: tick, Pending: true}
	fd, active, err := identify(r, boot, p, info)
	if err != nil || !active {
		return fail(errProcess)
	}
	defer unix.Close(fd)
	if err := saveReceipt(r); err != nil {
		return fail(err)
	}

	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(io.LimitReader(read, 4096))
		for scanner.Scan() {
			if scanner.Text() == "agent authenticated" {
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
		if !ok {
			return fail(errNotReady)
		}
	case <-timer.C:
		return fail(errNotReady)
	}
	if dead, err := exited(fd, 0); err != nil || dead {
		return fail(errNotReady)
	}
	r.Pending = false
	if err := saveReceipt(r); err != nil {
		return fail(err)
	}
	cmd.Process.Release()
	return nil
}
