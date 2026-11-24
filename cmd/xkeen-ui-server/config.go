package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"strings"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/server"
)

const defaultDBPath = "state/xkeen-ui-server.sqlite"

type config struct {
	listen             string
	dbPath             string
	tlsCert, tlsKey    string
	webOrigin          server.WebOrigin
	agentOrigin        server.WebOrigin
	validator          validatorConfig
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
	agentOrigin := flags.String("agent-origin", "", "explicit HTTPS origin for connecting router agents")
	webOrigin := flags.String("web-origin", "", "explicit browser HTTPS origin")
	flags.StringVar(&cfg.tlsKey, "tls-key", "", "PEM private key for the HTTPS listener")
	flags.StringVar(&cfg.validator.socket, "validator-socket", "", "absolute socket of the trusted local Xray validator")
	flags.StringVar(&cfg.validator.origin, "validator-origin", "", "explicit HTTPS origin of a trusted external Xray validator")
	flags.StringVar(&cfg.validator.ca, "validator-ca", "", "CA for the external validator")
	flags.StringVar(&cfg.validator.cert, "validator-client-cert", "", "mTLS client certificate for the external validator")
	flags.StringVar(&cfg.validator.key, "validator-client-key", "", "mTLS client key for the external validator")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(help)
			fmt.Fprintln(help, "Usage: xkeen-ui-server [--listen IP:port] [--db-path PATH] [--tls-cert PATH --tls-key PATH] [--web-origin HTTPS_ORIGIN]")
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
	var tlsFlag, webFlag, agentFlag, validatorFlag bool
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "db-path" {
			cfg.createDefaultDBDir = false
		}
		webFlag = webFlag || f.Name == "web-origin"
		agentFlag = agentFlag || f.Name == "agent-origin"
		validatorFlag = validatorFlag || strings.HasPrefix(f.Name, "validator-")
		tlsFlag = tlsFlag || f.Name == "tls-cert" || f.Name == "tls-key"
	})
	if tlsFlag && (strings.TrimSpace(cfg.tlsCert) == "" || strings.TrimSpace(cfg.tlsKey) == "") {
		return config{}, errors.New("invalid TLS configuration: both certificate and key files are required")
	}
	if webFlag {
		if cfg.tlsCert == "" {
			return config{}, errors.New("web origin requires HTTPS TLS configuration")
		}
		origin, err := server.ParseWebOrigin(*webOrigin)
		if err != nil {
			return config{}, err
		}
		cfg.webOrigin = origin
	}
	if agentFlag {
		if cfg.tlsCert == "" {
			return config{}, errors.New("agent origin requires HTTPS TLS configuration")
		}
		origin, err := server.ParseWebOrigin(*agentOrigin)
		if err != nil {
			return config{}, errors.New("invalid --agent-origin: expected an HTTPS origin without a path")
		}
		cfg.agentOrigin = origin
	}
	if validatorFlag && !cfg.validator.valid() {
		return config{}, errValidatorOptions
	}
	return cfg, nil
}
