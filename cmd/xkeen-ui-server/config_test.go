package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"default", nil, "127.0.0.1:8080"},
		{"allocated port", []string{"--listen", "127.0.0.1:0"}, "127.0.0.1:0"},
		{"ipv6", []string{"--listen", "[::1]:65535"}, "[::1]:65535"},
		{"wildcard v4", []string{"--listen=0.0.0.0:8081"}, "0.0.0.0:8081"},
		{"wildcard v6", []string{"--listen=[::]:0"}, "[::]:0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			got, err := parseArgs(tc.args, &output)
			if err != nil || got.listen != tc.want {
				t.Errorf("parseArgs = %#v, %v; want listen %q", got, err, tc.want)
			}
			if output.Len() != 0 {
				t.Errorf("unexpected help: %q", output.String())
			}
		})
	}
}

func TestParseInvalidArgs(t *testing.T) {
	for _, args := range [][]string{
		{"--db-path", ""}, {"--db-path", "  "}, {"--unknown"}, {"positional"}, {"--listen"}, {"--listen", ""},
		{"--listen", "127.0.0.1"}, {"--listen", "localhost:8080"},
		{"--listen", ":8080"}, {"--listen", "127.0.0.1:65536"},
		{"--listen", "127.0.0.1:-1"}, {"--listen", "127.0.0.1:http"},
		{"--listen", "127.0.0.1:+80"}, {"--listen", "not-an-ip:80"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var output bytes.Buffer
			_, err := parseArgs(args, &output)
			if err == nil || errors.Is(err, flag.ErrHelp) {
				t.Errorf("error = %v, want invalid arguments", err)
			}
		})
	}
}

func TestParseHelp(t *testing.T) {
	var output bytes.Buffer
	_, err := parseArgs([]string{"--help"}, &output)
	if !errors.Is(err, flag.ErrHelp) {
		t.Errorf("error = %v, want flag.ErrHelp", err)
	}
	for _, want := range []string{"listen", "127.0.0.1:8080", "db-path", "state/xkeen-ui-server.sqlite"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("help %q missing %q", output.String(), want)
		}
	}
}

func TestParseDBPath(t *testing.T) {
	for _, test := range []struct {
		args      []string
		path      string
		createDir bool
	}{
		{nil, "state/xkeen-ui-server.sqlite", true},
		{[]string{"--db-path", "state/xkeen-ui-server.sqlite"}, "state/xkeen-ui-server.sqlite", false},
		{[]string{"--db-path", "path with spaces?#.sqlite"}, "path with spaces?#.sqlite", false},
	} {
		got, err := parseArgs(test.args, &bytes.Buffer{})
		if err != nil || got.dbPath != test.path || got.createDefaultDBDir != test.createDir {
			t.Errorf("parseArgs(%q) = %#v, %v", test.args, got, err)
		}
	}
}

func TestAgentEnrollmentOriginRequiresExplicitHTTPS(t *testing.T) {
	base := []string{"--tls-cert", "cert", "--tls-key", "key"}
	for _, invalid := range []string{"", "http://agent.example", "https://agent.example/path", "https://secret@agent.example", "https://agent.example?secret"} {
		if _, err := parseArgs(append(base, "--agent-origin", invalid), io.Discard); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe acceptance or diagnostic %q: %v", invalid, err)
		}
	}
	if _, err := parseArgs([]string{"--agent-origin", "https://agent.example"}, io.Discard); err == nil {
		t.Fatal("origin accepted without TLS")
	}
	cfg, err := parseArgs(append(base, "--agent-origin", "https://AGENT.example:443"), io.Discard)
	if err != nil || cfg.agentOrigin != "https://agent.example" {
		t.Fatalf("valid origin: %q %v", cfg.agentOrigin, err)
	}
}
