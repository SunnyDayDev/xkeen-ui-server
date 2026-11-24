package restart

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestHotJournalPhases(t *testing.T) {
	for _, phase := range []Phase{HotDispatch, HotVerified, HotFiles} {
		if !validOriginalPhase(phase) {
			t.Fatal(phase)
		}
	}
	if validOriginalPhase("hot_unknown") {
		t.Fatal("accepted unknown phase")
	}
}

func TestHotAuthorityReopensAndRestoresEveryPhase(t *testing.T) {
	for _, phase := range []Phase{HotDispatch, HotVerified, HotFiles} {
		t.Run(string(phase), func(t *testing.T) {
			f := pendingFixture(t)
			files, e := hotFixture(t)
			e.Runtime.VerifierID = f.scope.VerifierID
			f.old = preparation.Baseline{Runtime: e.Runtime, Files: files}
			p := f.record.Pending
			p.Phase = phase
			p.Previous = f.old
			target := append([]preparation.BaselineFile(nil), files...)
			target[4].Content = bytes.ReplaceAll(files[4].Content, []byte("direct"), []byte("block"))
			p.TargetFiles = target
			for i, file := range target {
				p.Target.Snapshot.Files[i].Content = file.Content
			}
			p.Target.Tag, _ = agentrevision.Tag(f.scope.RouterID, p.Target.Snapshot)
			p.Hot = &HotReceipt{Guard: strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), Expected: []RuleRoute{{"deny", "block"}, {"switch", "block"}}, Outcome: "block", AllowPort: e.AllowPort, DenyPort: e.DenyPort, ProbePort: e.ProbePort}
			if phase == HotFiles {
				p.Started = &f.old.Runtime
			}
			// A hot attempt must retain its previously confirmed protected authority.
			previous := p.Target.Snapshot
			previous.Number = 1
			p.Target.Snapshot.Number = 2
			previous.Files = append([]configuration.CatalogFile(nil), previous.Files...)
			for i, file := range files {
				previous.Files[i].Content = file.Content
			}
			previousTag, _ := agentrevision.Tag(f.scope.RouterID, previous)
			p.Target.Tag, _ = agentrevision.Tag(f.scope.RouterID, p.Target.Snapshot)
			f.record.Current = &Confirmed{AttemptID: strings.Repeat("2", 32), Number: 1, Tag: previousTag, Baseline: f.old, Guard: p.Hot.Guard}
			dir := privateTestDirectory(t)
			j, err := OpenJournal(dir, f.scope)
			if err != nil {
				t.Fatal(err)
			}
			if err = j.Save(f.record); err != nil {
				t.Fatal(err)
			}
			j.Close()
			j, err = OpenJournal(dir, f.scope)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			got, err := j.Load()
			if err != nil || !reflect.DeepEqual(got, f.record) {
				t.Fatalf("reopened phase %s: %v", phase, err)
			}
			result, err := RestorePending(t.Context(), j, f, p.AttemptID)
			if err != nil || result.AttemptID != p.AttemptID || result.FailedPhase != phase || f.calls != 1 {
				t.Fatalf("recovery %v %v", result, err)
			}
			// A forged expected projection is inconsistent with the exact target.
			p.Hot.Expected[0].Outbound = "direct"
			if validRecord(f.scope, f.record) {
				t.Fatal("mismatched hot evidence accepted")
			}
		})
	}
}
