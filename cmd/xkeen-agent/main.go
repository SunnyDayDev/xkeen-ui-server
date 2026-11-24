package main

import (
	"context"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/agenthttp"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("xkeen-agent", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	origin := flags.String("server", "", "configured HTTPS server origin")
	stateDir := flags.String("state-dir", "", "private state directory")
	caFile := flags.String("ca-file", "", "optional PEM file with trusted certificates")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(stdout, "Usage: xkeen-agent --server HTTPS_ORIGIN --state-dir PATH [--ca-file PATH]")
		flags.SetOutput(stdout)
		flags.PrintDefaults()
		return 0
	} else if err != nil || flags.NArg() != 0 || *origin == "" || *stateDir == "" {
		fmt.Fprintln(stderr, "invalid agent arguments: HTTPS server and state directory are required")
		return 2
	}
	roots, err := loadTrustedRoots(*caFile)
	if err != nil {
		fmt.Fprintln(stderr, "invalid agent CA configuration")
		return 2
	}
	client, err := agenthttp.New(*origin, roots)
	if err != nil {
		fmt.Fprintln(stderr, "invalid agent HTTPS origin")
		return 2
	}
	defer client.CloseIdleConnections()
	store, err := statefile.Open(*stateDir)
	if err != nil {
		fmt.Fprintln(stderr, enrollment.ErrState)
		return 1
	}
	defer store.Close()

	if err := enrollment.Connect(ctx, client.Origin(), store, client); err != nil {
		if ctx.Err() != nil {
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "agent authenticated")
	<-ctx.Done()
	return 0
}

func loadTrustedRoots(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "agent CA")
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	const limit = 1024 * 1024
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid CA file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if len(raw) > limit || !roots.AppendCertsFromPEM(raw) {
		return nil, errors.New("invalid CA file")
	}
	return roots, nil
}
