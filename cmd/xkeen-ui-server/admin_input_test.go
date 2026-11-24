package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestAdminPrivatePasswordFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "password")
	if err := os.WriteFile(path, []byte("  synthetic long password \r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	password, err := readAdminPasswordFile(path)
	if err != nil || string(password) != "  synthetic long password " {
		t.Fatalf("private file did not preserve the password: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "pipe")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{link, fifo, dir} {
		if _, err := readAdminPasswordFile(name); err == nil {
			t.Fatal("accepted non-regular private input")
		}
	}
	hardlink := filepath.Join(dir, "hardlink")
	if err := os.Link(path, hardlink); err != nil {
		t.Fatal(err)
	}
	if _, err := readAdminPasswordFile(path); err == nil {
		t.Fatal("accepted a multiply linked private input")
	}
	if err := os.Remove(hardlink); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := readAdminPasswordFile(path); err == nil {
		t.Fatal("accepted a group-readable password")
	}
}
