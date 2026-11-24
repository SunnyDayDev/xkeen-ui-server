package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCatalogValidatorModes(t *testing.T) {
	remote := []string{"--validator-origin", "https://validator.example", "--validator-ca", "ca.pem",
		"--validator-client-cert", "client.pem", "--validator-client-key", "client.key"}
	for _, row := range []struct {
		name  string
		args  []string
		valid bool
	}{
		{"absent", nil, true},
		{"absolute Unix", []string{"--validator-socket", "/run/validator.sock"}, true},
		{"remote", remote, true},
		{"relative Unix", []string{"--validator-socket", "validator.sock"}, false},
		{"explicit empty", []string{"--validator-socket", ""}, false},
		{"modes mixed", append(append([]string{}, remote...), "--validator-socket", "/run/validator.sock"), false},
		{"missing client key", remote[:len(remote)-2], false},
	} {
		t.Run(row.name, func(t *testing.T) {
			_, err := parseArgs(row.args, io.Discard)
			if (err == nil) != row.valid {
				t.Fatalf("validator mode result: %v", err)
			}
		})
	}
}

func TestProcessValidatorBadTrustFailsBeforeStorage(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "server.sqlite")
	marker := filepath.Join(dir, "private-fixture-marker.pem")
	_, stderr, code := runBinaryInDir(t, dir, "--db-path", db, "--validator-origin", "https://validator.example",
		"--validator-ca", marker, "--validator-client-cert", marker, "--validator-client-key", marker)
	if code != 2 || !strings.Contains(stderr, "validator") || strings.Contains(stderr, "private-fixture-marker") {
		t.Fatalf("unsafe validation config result %d: %s", code, stderr)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("invalid validator reached SQLite: %v", err)
	}
}
