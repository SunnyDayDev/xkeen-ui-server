//go:build linux

package main

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/localvalidation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/restart"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
)

func runStartupRecoveryFixture(t *testing.T, binary string, path func(string) string, scope preparation.Scope, runtimeUID uint32,
	supervisor *process, launch func() *process, initial restart.Confirmed, target *publication.VerifiedFiles, checkTraffic func()) {
	t.Helper()
	kill := func() {
		t.Helper()
		if err := supervisor.cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		if err := supervisor.wait(t); err == nil {
			t.Fatal("expected terminated supervisor")
		}
	}
	loadJournal := func() (*restart.DiskJournal, restart.Record) {
		t.Helper()
		journal, err := restart.OpenJournal(path("journal"), scope)
		if err != nil {
			t.Fatal(err)
		}
		record, err := journal.Load()
		if err != nil {
			journal.Close()
			t.Fatal(err)
		}
		return journal, record
	}
	compete := func() {
		t.Helper()
		rival := startProcess(t, binary, "--config", path("profile.json"), "--application-config", path("application.json"))
		if err := rival.wait(t); err == nil || strings.Contains(rival.output(), "local application ready") {
			t.Fatal("competing process bypassed protected scope")
		}
	}
	// Pre-intent and committed-without-report cases both keep the existing process.
	compete()
	kill()
	supervisor = launch()
	checkTraffic()
	kill()
	journal, record := loadJournal()
	if record.Working() == nil || record.Current == nil || record.Current.AttemptID != initial.AttemptID ||
		record.Current.Baseline.Runtime != initial.Baseline.Runtime || record.Pending != nil {
		t.Fatal("stable baseline changed over restart")
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}

	snap := agentrevision.Snapshot{Number: initial.Number + 1, Profile: scope.Profile, Files: target.Files}
	tag, err := agentrevision.Tag(scope.RouterID, snap)
	if err != nil {
		t.Fatal(err)
	}
	desired := restart.Desired{Tag: tag, Snapshot: snap}
	stages := []restart.Phase{restart.Planned, restart.Stopped, restart.FilesVerified, restart.RuntimeStarted}
	for index, phase := range stages {
		journal, record = loadJournal()
		before := *record.Current
		files, err := restart.PlanWorkingGeneration(scope, before.Baseline, snap, runtimeUID)
		if err != nil {
			t.Fatal(err)
		}
		intent := &restart.Intent{AttemptID: fmt.Sprintf("%032x", index+1), Previous: before.Baseline, Target: desired, TargetFiles: files, Phase: phase}
		switch phase {
		case restart.Stopped, restart.FilesVerified:
			process, err := os.FindProcess(before.Baseline.Runtime.PID)
			if err != nil || process.Signal(syscall.SIGTERM) != nil {
				t.Fatal("could not stop attested fixture")
			}
			if phase == restart.Stopped {
				if err := os.Remove(path("work/06_extra.json")); err != nil {
					t.Fatal(err)
				}
			} else {
				for _, file := range files {
					name := path("work/" + file.Name)
					if err := os.WriteFile(name, file.Content, os.FileMode(file.Mode)); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Remove(path("work/06_extra.json")); err != nil {
					t.Fatal(err)
				}
			}
			waitSocketClosed(t, before.Baseline.Runtime.PID)
		case restart.RuntimeStarted:
			// Emulate an ambiguous hot/runtime receipt with the attested process alive.
			runtime := before.Baseline.Runtime
			runtime.StartTick++
			intent.Started = &runtime
		}
		record.Pending = intent
		if err := journal.Save(record); err != nil {
			journal.Close()
			t.Fatalf("%s durable attempt: %v", phase, err)
		}
		if err := journal.Close(); err != nil {
			t.Fatal(err)
		}
		if phase == restart.Stopped {
			// A missing central trust file prevents publication serving, but
			// cannot prevent restoration of the locally authorized baseline.
			ca := path("trusted-ca.pem")
			withheld := path("withheld-ca.pem")
			if err := os.Rename(ca, withheld); err != nil {
				t.Fatal(err)
			}
			offline := startProcess(t, binary, "--config", path("profile.json"), "--application-config", path("application.json"))
			if err := offline.wait(t); err == nil || strings.Contains(offline.output(), "local application ready") {
				t.Fatal("supervisor accepted application requests without its trust configuration")
			}
			checkTraffic()
			local, saved := loadJournal()
			if saved.Pending != nil || saved.LastRollback == nil || saved.LastRollback.AttemptID != intent.AttemptID {
				local.Close()
				t.Fatal("central trust dependency blocked local recovery")
			}
			if err := local.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(withheld, ca); err != nil {
				t.Fatal(err)
			}
		}
		supervisor = launch()
		checkTraffic()
		compete()
		kill()
		journal, record = loadJournal()
		if record.Pending != nil || record.LastRollback == nil || record.LastRollback.AttemptID != intent.AttemptID ||
			record.Current == nil || record.Current.AttemptID != initial.AttemptID || record.Current.Baseline.Runtime == before.Baseline.Runtime ||
			!reflect.DeepEqual(record.Current.Baseline.Files, initial.Baseline.Files) {
			t.Fatalf("%s did not re-attest the previous generation", phase)
		}
		if err := journal.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// Foreign data keeps the barrier closed and its bytes untouched.
	journal, record = loadJournal()
	files, err := restart.PlanWorkingGeneration(scope, record.Current.Baseline, snap, runtimeUID)
	if err != nil {
		t.Fatal(err)
	}
	record.Pending = &restart.Intent{AttemptID: fmt.Sprintf("%032x", 10), Previous: record.Current.Baseline, Target: desired, TargetFiles: files, Phase: restart.Planned}
	if err := journal.Save(record); err != nil {
		journal.Close()
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	foreignPath := path("work/external.toml")
	foreign := []byte("# synthetic conflict\n")
	if err := os.WriteFile(foreignPath, foreign, 0644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		blocked := startProcess(t, binary, "--config", path("profile.json"), "--application-config", path("application.json"))
		if err := blocked.wait(t); err == nil || !strings.Contains(blocked.output(), "rollback failed; current runtime unknown") {
			t.Fatalf("startup ignored conflict: %s", blocked.output())
		}
	}
	if actual, err := os.ReadFile(foreignPath); err != nil || !bytes.Equal(actual, foreign) {
		t.Fatal("conflicting data changed")
	}
	journal, record = loadJournal()
	defer journal.Close()
	if record.Pending == nil || record.Pending.Rollback == nil || record.Pending.Rollback.State != restart.RollbackFailed || record.Current.AttemptID != initial.AttemptID {
		t.Fatal("terminal attempt did not retain history and barrier")
	}
}

func waitSocketClosed(t *testing.T, pid int) {
	t.Helper()
	waitRecovery(t, 5*time.Second, "synthetic runtime did not exit", func() bool {
		_, err := localvalidation.ProcessStartTick(pid)
		return err != nil
	})
}
