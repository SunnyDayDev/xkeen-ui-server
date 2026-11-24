package localvalidation

import (
	"errors"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/restart"
)

type detachJournal struct {
	record restart.Record
}

func (j detachJournal) Load() (restart.Record, error) { return j.record, nil }

func (j detachJournal) Save(restart.Record) error {
	return errors.New("detach modified terminal journal")
}

func TestDetachRuntimeKeepsTerminalRollbackHeld(t *testing.T) {
	journal := detachJournal{record: restart.Record{Pending: &restart.Intent{
		AttemptID: strings.Repeat("a", 32),
		Rollback:  &restart.RollbackProgress{State: restart.RollbackFailed},
	}}}
	if err := (&RestartWorking{}).DetachRuntime(t.Context(), journal); !errors.Is(err, restart.ErrRollbackFailed) {
		t.Fatalf("terminal rollback returned %v", err)
	}
}
