package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/localvalidation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

func applicationReportHandoff(store *statefile.Store, binding enrollment.State) bool {
	entry, err := store.LoadReport()
	return err == nil && (entry == nil ||
		(entry.Origin == binding.Origin && entry.RouterID == binding.RouterID && entry.Sequence >= 0 && entry.Pending == nil))
}

func runApplicationReports(ctx context.Context, socket string, scope preparation.Scope, key string, wake <-chan struct{}, reconciled chan<- time.Time, out io.Writer) {
	backoff := time.Second
	notified := false
	for ctx.Err() == nil {
		delay := reporting.Interval
		delivered, err := localvalidation.ReconcileReport(ctx, socket, scope, key)
		if err != nil {
			if ctx.Err() == nil && !notified {
				fmt.Fprintln(out, "agent application report remains pending")
			}
			notified, delay = true, backoff
			backoff = min(2*backoff, reporting.Interval)
		} else {
			notified, backoff = false, time.Second
			if delivered {
				select {
				case reconciled <- time.Now():
				default:
				}
			}
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
