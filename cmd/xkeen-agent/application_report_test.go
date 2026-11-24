package main

import (
	"context"
	"os"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/contact"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

func TestApplicationReportHandoffHoldsLegacyQueue(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := statefile.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	binding := enrollment.State{Origin: "https://controller.example", RouterID: "router-example"}
	if _, allowed := applicationReportHandoff(store, binding); !allowed {
		t.Fatal("empty private state should permit the report-enabled profile")
	}
	entry := contact.Entry{Origin: binding.Origin, RouterID: binding.RouterID, Sequence: 9,
		Pending: &reporting.Report{Sequence: 9, Revision: 2, State: "applied"}}
	if err := store.SaveReport(entry); err != nil {
		t.Fatal(err)
	}
	if _, allowed := applicationReportHandoff(store, binding); allowed {
		t.Fatal("report-enabled profile can overtake a legacy pending report")
	}
	retained, err := store.LoadReport()
	if err != nil || retained.Pending == nil || *retained.Pending != *entry.Pending {
		t.Fatal("handoff altered the pending D06 report")
	}
	entry.Pending = nil
	if err := store.SaveReport(entry); err != nil {
		t.Fatal(err)
	}
	if floor, allowed := applicationReportHandoff(store, binding); !allowed || floor != 9 {
		t.Fatal("confirmed legacy queue should retain the local floor")
	}
	entry.RouterID = "elsewhere-example"
	if err := store.SaveReport(entry); err != nil {
		t.Fatal(err)
	}
	if _, allowed := applicationReportHandoff(store, binding); allowed {
		t.Fatal("foreign queue was used in report-enabled mode")
	}
}

func TestApplicationReportCursorRejectsRegressedLegacyFloor(t *testing.T) {
	read := func(seq int64) func(context.Context) (int64, error) {
		return func(context.Context) (int64, error) { return seq, nil }
	}
	if err := applicationReportCursor(t.Context(), 9, read(8)); err != errReportCursorRegressed {
		t.Fatal("regressed server cursor allowed handoff", err)
	}
	if err := applicationReportCursor(t.Context(), 9, read(9)); err != nil {
		t.Fatal("matching server floor blocked handoff", err)
	}
}
