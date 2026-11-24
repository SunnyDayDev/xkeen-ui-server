package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/revisionwatch"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/server"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) (exitCode int) {
	if len(args) > 0 && args[0] == "admin" {
		return runAdmin(args[1:], os.Stdin, stdout, stderr)
	}
	if len(args) > 0 && args[0] == "storage" {
		return runStorage(args[1:], stdout, stderr)
	}
	cfg, err := parseArgs(args, stdout)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var tlsConfig *tls.Config
	if cfg.tlsCert != "" {
		certificate, err := tls.LoadX509KeyPair(cfg.tlsCert, cfg.tlsKey)
		if err != nil {
			fmt.Fprintln(stderr, "invalid TLS configuration: cannot load certificate and key")
			return 2
		}
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	}
	validator, err := cfg.validator.open()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.createDefaultDBDir {
		if err := os.MkdirAll(filepath.Dir(cfg.dbPath), 0700); err != nil {
			fmt.Fprintf(stderr, "open SQLite: prepare database directory: %v\n", err)
			return 1
		}
	}
	db, err := sqlite.Open(ctx, cfg.dbPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() {
		if err := db.Close(); err != nil {
			fmt.Fprintf(stderr, "close SQLite: %v\n", err)
			exitCode = 1
		}
	}()
	listener, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		fmt.Fprintf(stderr, "listen: %v\n", err)
		return 1
	}
	handler := server.NewHandler()
	publications := sqlite.NewPublications(db)
	if tlsConfig != nil {
		listener = tls.NewListener(listener, tlsConfig)
		watch := revisionwatch.New(ctx, publications, 25*time.Second, 128)
		publications = sqlite.NewPublications(db, watch.Notify)
		credentials := agentcredentials.NewService(sqlite.NewAgentCredentials(db, watch.Notify), time.Now)
		handler = server.NewAgentHandler(credentials, publications, sqlite.NewContacts(db, time.Now), watch)
	}
	if cfg.webOrigin != "" {
		webStore := sqlite.NewWebTemplates(db)
		sessions := webadmin.New(sqlite.NewWebAdmin(db), time.Now)
		handler = server.NewWebShellHandler(cfg.webOrigin, handler)
		preparation := webadmin.NewRouterPreparation(sqlite.NewWebRouterPreparation(publications), validator, time.Now)
		handler = server.NewWebRouterPreparationHandler(ctx, cfg.webOrigin, preparation, sessions, handler)
		enrollment := webadmin.NewRouterEnrollment(sqlite.NewWebRouterEnrollment(db), time.Now)
		handler = server.NewWebRouterEnrollmentHandler(cfg.webOrigin, cfg.agentOrigin, enrollment, sessions, handler)
		overview := webadmin.NewRouterOverview(sqlite.NewWebRouterStatus(db), time.Now)
		handler = server.NewWebRouterStatusHandler(cfg.webOrigin, overview, handler)
		variables := webadmin.NewRouterVariableEditor(sqlite.NewWebRouterDrafts(db), time.Now)
		handler = server.NewWebRouterVariableHandler(cfg.webOrigin, variables, sessions, handler)
		handler = server.NewWebTemplatesHandler(cfg.webOrigin, webadmin.NewTemplates(webStore, time.Now), handler)
		handler = server.NewWebTemplateEditorHandler(cfg.webOrigin, webadmin.NewTemplateEditor(webStore, time.Now), sessions, handler)
		handler = server.NewWebAdminHandler(cfg.webOrigin, sessions, handler)
	}
	external := sqlite.NewExternalAccess(db, time.Now)
	handler = server.NewExternalAccessHandler(cfg.webOrigin, external, server.NewExternalTemplates(external), handler)
	if err := json.NewEncoder(stderr).Encode(struct {
		Event   string `json:"event"`
		Address string `json:"address"`
	}{Event: "listening", Address: listener.Addr().String()}); err != nil {
		_ = listener.Close()
		fmt.Fprintf(stderr, "report listener: %v\n", err)
		return 1
	}
	if err := server.Serve(ctx, listener, handler, 5*time.Second); err != nil {
		fmt.Fprintf(stderr, "server: %v\n", err)
		return 1
	}
	return 0
}
