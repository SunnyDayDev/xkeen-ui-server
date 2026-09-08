package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
)

type config struct {
	listen string
}

func parseArgs(args []string, help io.Writer) (config, error) {
	var cfg config
	flags := flag.NewFlagSet("xkeen-ui-server", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	flags.StringVar(&cfg.listen, "listen", "127.0.0.1:8080", "TCP listen address (IP literal:port)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(help)
			fmt.Fprintln(help, "Usage: xkeen-ui-server [--listen IP:port]")
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
	return cfg, nil
}
