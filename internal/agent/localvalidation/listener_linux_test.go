//go:build linux

package localvalidation

import (
	"net"
	"os"
	"os/exec"
	"testing"
)

func TestListenerMustBelongToAttestedPID(t *testing.T) {
	own, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer own.Close()
	foreign := exec.Command("/bin/sleep", "10")
	if err := foreign.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = foreign.Process.Kill(); _ = foreign.Wait() }()
	if !ownsListener(os.Getpid(), own.Addr().String()) || ownsListener(foreign.Process.Pid, own.Addr().String()) {
		t.Fatal("configured socket is not bound to the attested process")
	}
}
