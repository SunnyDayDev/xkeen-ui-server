package main

import (
	"bytes"
	"errors"
	"flag"
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
		{"--unknown"}, {"positional"}, {"--listen"}, {"--listen", ""},
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
	for _, want := range []string{"listen", "127.0.0.1:8080"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("help %q missing %q", output.String(), want)
		}
	}
}
