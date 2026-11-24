//go:build linux

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/localvalidation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == localvalidation.WorkerFlag {
		os.Exit(localvalidation.Worker(os.Args[2:]))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "local validation is unavailable")
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("xkeen-local-validator", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "root-owned local supervisor profile")
	manifestDir := flags.String("working-manifest", "", "record a trusted working directory manifest")
	process := flags.Int("working-process", 0, "record start tick of a trusted launch")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || os.Geteuid() != 0 {
		return preparation.ErrUnavailable
	}
	switch {
	case *manifestDir != "" && *configPath == "" && *process == 0:
		manifest, err := localvalidation.WorkingManifest(*manifestDir)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, manifest)
		return err
	case *process > 0 && *configPath == "" && *manifestDir == "":
		tick, err := localvalidation.ProcessStartTick(*process)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, tick)
		return err
	case *configPath == "" || *manifestDir != "" || *process != 0:
		return preparation.ErrUnavailable
	}
	cfg, err := localvalidation.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	runner := cfg.Runner()
	lease, err := runner.Acquire(ctx)
	if err != nil {
		return err
	}
	defer lease.Close()
	listener, err := lease.Listen(cfg.Socket, cfg.AgentUID, cfg.AgentGID)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{
		Handler:           localvalidation.Handler(cfg.Scope, cfg.Observer(), runner),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 4 * time.Second, WriteTimeout: 18 * time.Second, MaxHeaderBytes: 4096,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	fmt.Fprintln(out, "local validator ready")
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err := server.Shutdown(shutdown)
		if serveErr := <-done; !errors.Is(serveErr, http.ErrServerClosed) {
			return preparation.ErrUnavailable
		}
		return err
	}
}
