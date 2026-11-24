package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"strings"
)

const defaultDBPath = "state/xkeen-ui-server.sqlite"

type config struct {
	listen             string
	dbPath             string
	createDefaultDBDir bool
}

func parseArgs(args []string, help io.Writer) (config, error) {
	var cfg config
	flags := flag.NewFlagSet("xkeen-ui-server", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	flags.StringVar(&cfg.listen, "listen", "127.0.0.1:8080", "TCP listen address (IP literal:port)")
	flags.StringVar(&cfg.dbPath, "db-path", defaultDBPath, "path to the SQLite database file")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(help)
			fmt.Fprintln(help, "Usage: xkeen-ui-server [--listen IP:port] [--db-path PATH]")
			flags.PrintDefaults()
		}
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, errors.New("positional arguments are not supported")
	}
	if _, err := netip.ParseAddrPort(cfg.listen); err != nil {
		return config{}, fmt.Errorf("invalid --listen: %w", err)
	}
	if strings.TrimSpace(cfg.dbPath) == "" {
		return config{}, errors.New("invalid --db-path: path must be nonempty")
	}
	cfg.createDefaultDBDir = true
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "db-path" {
			cfg.createDefaultDBDir = false
		}
	})
	return cfg, nil
}
