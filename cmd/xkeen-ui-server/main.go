package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/server"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseArgs(args, stdout)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		fmt.Fprintf(stderr, "listen: %v\n", err)
		return 1
	}
	if err := json.NewEncoder(stderr).Encode(struct {
		Event   string `json:"event"`
		Address string `json:"address"`
	}{Event: "listening", Address: listener.Addr().String()}); err != nil {
		_ = listener.Close()
		fmt.Fprintf(stderr, "report listener: %v\n", err)
		return 1
	}
	if err := server.Serve(ctx, listener, server.NewHandler(), 5*time.Second); err != nil {
		fmt.Fprintf(stderr, "server: %v\n", err)
		return 1
	}
	return 0
}
