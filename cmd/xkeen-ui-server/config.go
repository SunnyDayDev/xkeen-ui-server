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
	tlsCert, tlsKey    string
	createDefaultDBDir bool
}

func parseArgs(args []string, help io.Writer) (config, error) {
	var cfg config
	flags := flag.NewFlagSet("xkeen-ui-server", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	flags.StringVar(&cfg.listen, "listen", "127.0.0.1:8080", "TCP listen address (IP literal:port)")
	flags.StringVar(&cfg.dbPath, "db-path", defaultDBPath, "path to the SQLite database file")
	flags.StringVar(&cfg.tlsCert, "tls-cert", "", "PEM certificate chain for the HTTPS listener")
	flags.StringVar(&cfg.tlsKey, "tls-key", "", "PEM private key for the HTTPS listener")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(help)
			fmt.Fprintln(help, "Usage: xkeen-ui-server [--listen IP:port] [--db-path PATH] [--tls-cert PATH --tls-key PATH]")
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
	var tlsFlag bool
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "db-path" {
			cfg.createDefaultDBDir = false
		}
		tlsFlag = tlsFlag || f.Name == "tls-cert" || f.Name == "tls-key"
	})
	if tlsFlag && (strings.TrimSpace(cfg.tlsCert) == "" || strings.TrimSpace(cfg.tlsKey) == "") {
		return config{}, errors.New("invalid TLS configuration: both certificate and key files are required")
	}
	return cfg, nil
}
