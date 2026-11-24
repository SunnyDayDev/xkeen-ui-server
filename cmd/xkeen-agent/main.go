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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/agenthttp"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/contact"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/desired"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/revisionloop"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
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
	validatorSocket := flags.String("local-validator-socket", "", "protected local preparation supervisor socket")
	applicationSocket := flags.String("local-application-socket", "", "protected local controlled application socket")
	applicationReports := flags.Bool("local-application-reports", false, "use the v1 report-enabled local application profile")
	verifier := flags.String("local-verifier", "", "trusted local preparation profile ID")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(stdout, "Usage: xkeen-agent --server HTTPS_ORIGIN --state-dir PATH [--ca-file PATH] [--local-validator-socket PATH | --local-application-socket PATH] [--local-verifier ID] [--local-application-reports]")
		flags.SetOutput(stdout)
		flags.PrintDefaults()
		return 0
	} else if err != nil || flags.NArg() != 0 || *origin == "" || *stateDir == "" {
		fmt.Fprintln(stderr, "invalid agent arguments: HTTPS server and state directory are required")
		return 2
	}
	if (*applicationReports && *applicationSocket == "") || (*validatorSocket != "" && *applicationSocket != "") || (*validatorSocket == "" && *applicationSocket == "") != (*verifier == "") {
		fmt.Fprintln(stderr, "invalid local preparation profile")
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
	binding, err := store.Load()
	if err != nil || binding == nil {
		fmt.Fprintln(stderr, enrollment.ErrState)
		return 1
	}
	reportSocket := *applicationSocket
	var legacyFloor int64
	var reportReady atomic.Bool
	if *applicationReports {
		floor, allowed := applicationReportHandoff(store, *binding)
		if !allowed {
			reportSocket = ""
			fmt.Fprintln(stderr, "agent legacy report needs resolution before enabling application reports")
		}
		legacyFloor = floor
		reportReady.Store(allowed && floor == 0)
	}
	contactContext, stopContact := context.WithCancel(ctx)
	var workers sync.WaitGroup
	queue := contact.New(*binding, store, client)
	var wake chan struct{}
	var reconciled chan time.Time
	if *applicationReports {
		workers.Go(func() { queue.RunHeartbeat(contactContext) })
		if reportSocket != "" {
			wake = make(chan struct{}, 1)
			reconciled = make(chan time.Time, 1)
			reportScope := preparation.Scope{Origin: binding.Origin, RouterID: binding.RouterID,
				Profile: configuration.XrayIdentityVersion, VerifierID: *verifier}
			workers.Go(func() {
				handoff := func(ctx context.Context) (bool, error) {
					if reportReady.Load() {
						return false, nil
					}
					if err := applicationReportCursor(ctx, legacyFloor, func(ctx context.Context) (int64, error) {
						return client.Cursor(ctx, binding.Key)
					}); err != nil {
						return false, err
					}
					reportReady.Store(true)
					return true, nil
				}
				runApplicationReports(contactContext, reportSocket, reportScope, binding.Key, handoff, wake, reconciled, stderr)
			})
		}
	} else {
		workers.Go(func() {
			queue.Run(contactContext, func(error) {
				fmt.Fprintln(stderr, "agent report remains pending")
			})
		})
	}
	defer func() {
		stopContact()
		workers.Wait()
	}()
	snapshot, err := desired.UntilCurrent(ctx, *binding, store, client)
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	prepare := candidatePreparation{socket: *validatorSocket, application: reportSocket, applicationReports: *applicationReports, applicationReady: reportReady.Load, reportWake: wake, profile: *verifier, binding: *binding, store: store, out: stdout, diagnostics: stderr}
	prepare.check(ctx, snapshot)
	fmt.Fprintln(stdout, "agent authenticated")
	var latest int64
	if snapshot != nil {
		latest = snapshot.Number
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	err = revisionloop.Run(ctx, binding.Key, latest, revisionTicks(ctx, ticker.C, reconciled), client, func(ctx context.Context) (*agentrevision.Snapshot, error) {
		snapshot, err := desired.Sync(ctx, *binding, store, client)
		if err == nil {
			prepare.check(ctx, snapshot)
		}
		return snapshot, err
	})
	if ctx.Err() != nil {
		return 0
	}
	fmt.Fprintln(stderr, err)
	return 1
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
