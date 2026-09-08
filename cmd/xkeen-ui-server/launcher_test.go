package main

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestLauncher(t *testing.T) {
	p := startProcess(t, "sh", "scripts/run-server.sh", "--listen", "127.0.0.1:0")
	address := p.ready(t)
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := p.wait(t); err != nil {
		t.Fatalf("launcher exit: %v; %s", err, p.output())
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("server still running after launcher exit: %v", err)
	}
	_ = listener.Close()
}

func TestLauncherBuildFailure(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\nexit 23\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "scripts/run-server.sh")
	cmd.Dir = projectRoot
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("launcher ignored build failure: %v", ctx.Err())
	}
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 23 {
		t.Errorf("build failure exit = %v, want 23; %s", err, output)
	}
}
