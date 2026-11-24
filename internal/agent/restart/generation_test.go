package restart

import (
	"slices"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestPlanWorkingGenerationUsesTrustedMetadata(t *testing.T) {
	f := newApplyFixture(t)
	for i := range f.old.Files {
		f.old.Files[i].Mode = 0644
	}
	target := f.first.Snapshot
	target.Files = append(slices.Clone(target.Files), configuration.CatalogFile{Name: "06_extra.json", Content: []byte("{}")})
	files, err := PlanWorkingGeneration(f.scope, f.old, target, 103)
	if err != nil || len(files) != 6 || files[0].Mode != 0644 || files[5].Mode != 0640 || files[5].GID != 103 || files[5].UID != 0 {
		t.Fatal("new optional file did not receive trusted local metadata", err)
	}
	f.old.Files = files
	after, err := PlanWorkingGeneration(f.scope, f.old, f.first.Snapshot, 103)
	if err != nil || len(after) != 5 {
		t.Fatal("obsolete optional file remained in planned generation", err)
	}
	f.old.Files = append(f.old.Files, preparation.BaselineFile{Name: "outside.json", Content: []byte("{}"), Mode: 0644})
	if _, err := PlanWorkingGeneration(f.scope, f.old, f.first.Snapshot, 103); err == nil {
		t.Fatal("unmanaged file was accepted")
	}
}
