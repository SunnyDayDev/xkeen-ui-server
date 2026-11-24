package restart

import "context"

// StartupWorking is the controlled launch path for the same scope as the journal.
type StartupWorking interface {
	Restorer
	StartCommitted(context.Context, Journal) error
}

// RecoverStartup resolves the protected journal before ordinary startup.
// The caller holds the journal and working-scope locks across this operation.
func RecoverStartup(ctx context.Context, journal Journal, working StartupWorking) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	record, err := journal.Load()
	if err != nil {
		return err
	}
	if record.Pending != nil {
		// Planned alone cannot prove that the stop command had no effect.
		if _, err := RestorePending(ctx, journal, working, record.Pending.AttemptID); err != nil {
			return err
		}
	}
	return working.StartCommitted(ctx, journal)
}
