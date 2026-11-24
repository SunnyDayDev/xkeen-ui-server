package restart

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

func TestReportRetainsAttemptAcrossLostReplyAndOwnerRestart(t *testing.T) {
	f := reportingFixture(t)
	remote := &reportStub{floor: 6, fail: true}
	makeService := func() *Service { return New(f.scope, f, f, f, f) }
	if _, err := makeService().ReconcileReport(t.Context(), remote, "fixture", false); err == nil {
		t.Fatal("lost response was treated as confirmed")
	}
	if len(remote.offered) != 1 || remote.offered[0].Sequence != 7 || remote.offered[0].State != "applied" ||
		f.record.Delivery == nil || f.record.Delivery.Body == nil || f.record.Delivery.Done {
		t.Fatal("attempt and original report were not left pending")
	}
	remote.fail = false
	if _, err := makeService().ReconcileReport(t.Context(), remote, "fixture", false); err != nil {
		t.Fatal(err)
	}
	if _, err := makeService().ReconcileReport(t.Context(), remote, "fixture", false); err != nil {
		t.Fatal(err)
	}
	if len(remote.offered) != 2 || remote.offered[1] != remote.offered[0] || !f.record.Delivery.Done {
		t.Fatal("lost acknowledgement did not reproduce the same cursor and fields")
	}
}

func TestReportWithoutFreshProofDoesNotSendApplied(t *testing.T) {
	for _, assigned := range []bool{false, true} {
		f := reportingFixture(t)
		remote := &reportStub{}
		svc := New(f.scope, f, f, f, f)
		if assigned {
			remote.fail = true
			remote.beforeCommit = true
			if _, err := svc.ReconcileReport(t.Context(), remote, "fixture", false); err == nil {
				t.Fatal("temporary unavailability was treated as an acknowledgement")
			}
			remote.offered = nil
			remote.fail = false
		}
		f.observeErr = ErrBaseline
		if _, err := svc.ReconcileReport(t.Context(), remote, "fixture", false); err != nil {
			t.Fatal(err)
		}
		if len(remote.offered) != 1 || remote.offered[0].State != "failed" || remote.offered[0].Code != "runtime_unknown" ||
			f.record.Current == nil || *f.record.Delivery.Body != remote.offered[0] {
			t.Fatal("stale proof produced a fake success or lost its history")
		}
	}
}

func TestReportConflictNeverReallocatesOrAllowsApply(t *testing.T) {
	f := reportingFixture(t)
	remote := &reportStub{contactError: reporting.ErrConflict}
	svc := New(f.scope, f, f, f, f)
	for range 2 {
		if _, err := svc.ReconcileReport(t.Context(), remote, "fixture", false); !errors.Is(err, reporting.ErrConflict) {
			t.Fatal(err)
		}
	}
	if len(remote.offered) != 2 || remote.offered[0] != remote.offered[1] || f.record.Delivery.Done {
		t.Fatal("conflict changed reserved body")
	}
	f.effects = nil
	if _, err := svc.Apply(t.Context(), "fixture", f.proposed); err != ErrReportPending || len(f.effects) != 0 {
		t.Fatal("unresolved delivery allowed a mutating attempt", err)
	}
}

func TestReportOnlyAfterTerminalRollback(t *testing.T) {
	f := reportingFixture(t)
	f.record.Pending = &Intent{AttemptID: "01010101010101010101010101010101", Target: f.first, Rollback: &RollbackProgress{State: RollbackFailed}}
	f.record.Delivery = nil
	if err := EnableReports(f); err != nil {
		t.Fatal(err)
	}
	remote := &reportStub{cursorError: errors.New("unavailable")}
	f.observeErr = errors.New("live runtime unavailable")
	svc := New(f.scope, f, f, f, f)
	if _, err := svc.ReconcileReport(t.Context(), remote, "fixture", true); err == nil || len(remote.offered) != 0 {
		t.Fatal("unavailable server lost report-only state")
	}
	remote.cursorError = nil
	if _, err := svc.ReconcileReport(t.Context(), remote, "fixture", true); err != nil {
		t.Fatal(err)
	}
	if len(remote.offered) != 1 || remote.offered[0].Code != "rollback_failed" || f.record.Pending == nil {
		t.Fatal("report-only changed the pending startup barrier or offered success")
	}
}

func reportingFixture(t *testing.T) *applyFixture {
	t.Helper()
	f := newApplyFixture(t)
	f.record.Current = &Confirmed{AttemptID: "02020202020202020202020202020202", Number: f.first.Snapshot.Number,
		Tag: f.first.Tag, Baseline: preparation.Baseline{Runtime: f.old.Runtime, Files: slices.Clone(f.old.Files)}}
	if err := EnableReports(f); err != nil {
		t.Fatal(err)
	}
	return f
}

type reportStub struct {
	floor                     int64
	offered                   []reporting.Report
	fail, beforeCommit        bool
	cursorError, contactError error
	contactHook               func()
	identityErr               error
	cursorHook                func()
}

func (r *reportStub) Identity(context.Context, string) (string, error) {
	return "router-example", r.identityErr
}
func (r *reportStub) Cursor(context.Context, string) (int64, error) {
	if r.cursorHook != nil {
		r.cursorHook()
	}
	return r.floor, r.cursorError
}
func (r *reportStub) Contact(_ context.Context, _ string, report *reporting.Report) error {
	r.offered = append(r.offered, *report)
	if r.contactHook != nil {
		r.contactHook()
	}
	if r.contactError != nil {
		return r.contactError
	}
	if !r.beforeCommit || !r.fail {
		r.floor = max(r.floor, report.Sequence)
	}
	if r.fail {
		return errors.New("lost report acknowledgement")
	}
	return nil
}

func TestReportAfterAcknowledgementMarksNewUncertaintyOnce(t *testing.T) {
	f := reportingFixture(t)
	remote := &reportStub{}
	for _, err := range []error{nil, ErrBaseline, ErrBaseline} {
		f.observeErr = err
		if _, err := New(f.scope, f, f, f, f).ReconcileReport(t.Context(), remote, "fixture", false); err != nil {
			t.Fatal(err)
		}
	}
	if len(remote.offered) != 2 || remote.offered[0].State != "applied" ||
		remote.offered[1].Code != "runtime_unknown" || remote.offered[1].Sequence != 2 {
		t.Fatal("proof loss changed historical acknowledgement or duplicated the diagnostic")
	}
}

func TestReportPendingSurvivesClosedJournal(t *testing.T) {
	f := reportingFixture(t)
	dir := privateTestDirectory(t)
	journal, err := OpenJournal(dir, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Save(f.record); err != nil {
		t.Fatal(err)
	}
	remote := &reportStub{fail: true}
	if _, err := New(f.scope, f, journal, f, f).ReconcileReport(t.Context(), remote, "fixture", false); err == nil {
		t.Fatal("lost response was acknowledged")
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := OpenJournal(dir, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	remote.fail = false
	if _, err := New(f.scope, f, recovered, f, f).ReconcileReport(t.Context(), remote, "fixture", false); err != nil {
		t.Fatal(err)
	}
	if len(remote.offered) != 2 || remote.offered[0] != remote.offered[1] {
		t.Fatal("durable replay changed report")
	}
}

func TestReportAcknowledgementFailedSaveKeepsSamePending(t *testing.T) {
	f := reportingFixture(t)
	base := f.record
	remote := &reportStub{}
	rejectAck := &reportJournal{f: f, failAcknowledgement: true}
	makeService := func(j Journal) *Service { return New(f.scope, f, j, f, f) }
	if _, err := makeService(rejectAck).ReconcileReport(t.Context(), remote, "fixture", false); err != ErrJournal {
		t.Fatal("acknowledgement save failure did not propagate", err)
	}
	if len(remote.offered) != 1 || f.record.Delivery.Done || f.record.Delivery.Body == nil ||
		f.record.Delivery.Outcome.AttemptID != base.Current.AttemptID {
		t.Fatal("failed acknowledgement lost pending result")
	}
	if _, err := makeService(f).ReconcileReport(t.Context(), remote, "fixture", false); err != nil {
		t.Fatal(err)
	}
	if len(remote.offered) != 2 || remote.offered[0] != remote.offered[1] {
		t.Fatal("legacy high-water did not suppress a new allocation")
	}
}

type reportJournal struct {
	f                   *applyFixture
	failAcknowledgement bool
}

func (j *reportJournal) Load() (Record, error) { return j.f.Load() }

func (j *reportJournal) Save(record Record) error {
	if j.failAcknowledgement && record.Delivery != nil && record.Delivery.Done {
		return ErrJournal
	}
	return j.f.Save(record)
}

func TestOneApplicationWaitsForInFlightReportWithoutLosingDesired(t *testing.T) {
	f := reportingFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	remote := &reportStub{contactHook: func() { close(started); <-release }}
	svc := New(f.scope, f, f, f, f)
	reported := make(chan error, 1)
	go func() {
		_, err := svc.ReconcileReport(t.Context(), remote, "fixture", false)
		reported <- err
	}()
	select {
	case <-started:
	case err := <-reported:
		t.Fatal("report did not reach delivery", err)
	}
	applied := make(chan error, 1)
	go func() {
		_, err := svc.Apply(t.Context(), "fixture", f.proposed)
		applied <- err
	}()
	close(release)
	if err := <-reported; err != nil {
		t.Fatal(err)
	}
	if err := <-applied; err != nil {
		t.Fatal("in-flight report lost a desired application", err)
	}
}

func TestReportAfterLostRuntimeCanAcknowledgeAConsumedSuccess(t *testing.T) {
	f := reportingFixture(t)
	remote := &reportStub{fail: true}
	makeService := func() *Service { return New(f.scope, f, f, f, f) }
	if _, err := makeService().ReconcileReport(t.Context(), remote, "fixture", false); err == nil {
		t.Fatal("late acceptance unexpectedly acknowledged")
	}
	f.observeErr = ErrBaseline
	remote.fail = false
	if _, err := makeService().ReconcileReport(t.Context(), remote, "fixture", false); err != nil {
		t.Fatal(err)
	}
	if len(remote.offered) != 3 || remote.offered[0] != remote.offered[1] ||
		remote.offered[2].Sequence != 2 || remote.offered[2].Code != "runtime_unknown" {
		t.Fatal("loss of proof changed consumed success or confused it with new success")
	}
}

func TestReportAuthorityLossKeepsReservedBody(t *testing.T) {
	f := reportingFixture(t)
	remote := &reportStub{fail: true}
	svc := New(f.scope, f, f, f, f)
	_, _ = svc.ReconcileReport(t.Context(), remote, "fixture", false)
	before := f.record.Delivery
	remote.identityErr = errors.New("revoked")
	for range 2 {
		if _, err := svc.ReconcileReport(t.Context(), remote, "fixture", false); err != ErrReportAuthority {
			t.Fatal("unauthorized report was considered valid", err)
		}
	}
	if len(remote.offered) != 1 || f.record.Delivery != before {
		t.Fatal("revocation replaced the stored report")
	}
}

func TestReportRechecksProofAfterCursorAndReservation(t *testing.T) {
	f := reportingFixture(t)
	remote := &reportStub{cursorHook: func() { f.observeErr = ErrBaseline }}
	if _, err := New(f.scope, f, f, f, f).ReconcileReport(t.Context(), remote, "fixture", false); err != nil {
		t.Fatal(err)
	}
	if len(remote.offered) != 1 || remote.offered[0].State == "applied" ||
		remote.offered[0].Code != "runtime_unknown" || remote.offered[0].Sequence != 2 {
		t.Fatal("proof lost during allocation allowed a first success")
	}
}
