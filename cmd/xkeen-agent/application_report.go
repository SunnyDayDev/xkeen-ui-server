package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/localvalidation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

func applicationReportHandoff(store *statefile.Store, binding enrollment.State) (int64, bool) {
	entry, err := store.LoadReport()
	if err != nil {
		return 0, false
	}
	if entry == nil {
		return 0, true
	}
	if entry.Origin != binding.Origin || entry.RouterID != binding.RouterID || entry.Sequence < 0 || entry.Pending != nil {
		return 0, false
	}
	return entry.Sequence, true
}

var errReportCursorRegressed = errors.New("agent application reporting is blocked by a regressed server cursor")

func applicationReportCursor(ctx context.Context, floor int64, read func(context.Context) (int64, error)) error {
	current, err := read(ctx)
	if err != nil {
		return err
	}
	if current < floor {
		return errReportCursorRegressed
	}
	return nil
}

func runApplicationReports(ctx context.Context, socket string, scope preparation.Scope, key string, handoff func(context.Context) (bool, error), wake <-chan struct{}, reconciled chan<- time.Time, out io.Writer) {
	backoff := time.Second
	notified := false
	for ctx.Err() == nil {
		delay := reporting.Interval
		ready, err := handoff(ctx)
		var delivered bool
		if err == nil {
			delivered, err = localvalidation.ReconcileReport(ctx, socket, scope, key)
		}
		if ready || delivered {
			select {
			case reconciled <- time.Now():
			default:
			}
		}
		if err != nil {
			if ctx.Err() == nil && !notified {
				if errors.Is(err, errReportCursorRegressed) {
					fmt.Fprintln(out, errReportCursorRegressed)
				} else {
					fmt.Fprintln(out, "agent application report remains pending")
				}
			}
			notified, delay = true, backoff
			backoff = min(2*backoff, reporting.Interval)
		} else {
			notified, backoff = false, time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
		case <-wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func revisionTicks(ctx context.Context, regular, reports <-chan time.Time) <-chan time.Time {
	if reports == nil {
		return regular
	}
	ticks := make(chan time.Time, 1)
	go func() {
		defer close(ticks)
		for {
			var tick time.Time
			select {
			case <-ctx.Done():
				return
			case tick = <-regular:
			case tick = <-reports:
			}
			select {
			case ticks <- tick:
			default:
			}
		}
	}()
	return ticks
}
