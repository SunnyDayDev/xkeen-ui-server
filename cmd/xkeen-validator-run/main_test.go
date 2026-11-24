package main

import (
	"context"
	"testing"
	"time"
)

func TestCommandLifetimeAndExit(t *testing.T) {
	if got := run(context.Background(), "/bin/sh", []string{"-c", "exit 23"}); got != 23 {
		t.Fatal(got)
	}
	if got := run(context.Background(), "/unavailable-test-command", nil); got != 125 {
		t.Fatal(got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if got := run(ctx, "/bin/sleep", []string{"10"}); got != 124 {
		t.Fatal(got)
	}
}
