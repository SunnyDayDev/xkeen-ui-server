package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/localvalidation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/restart"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/statefile"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

// The desired loop calls check serially after a successful scoped D05 read.
type candidatePreparation struct {
	socket, application, profile, lastTag string
	binding                               enrollment.State
	store                                 *statefile.Store
	out, diagnostics                      io.Writer
	applicationReports                    bool
	reportWake                            chan<- struct{}
	applicationReady                      func() bool
}

func (p *candidatePreparation) check(ctx context.Context, snapshot *agentrevision.Snapshot) {
	if (p.socket == "" && p.application == "") || snapshot == nil || ctx.Err() != nil {
		return
	}
	tag, err := agentrevision.Tag(p.binding.RouterID, *snapshot)
	if err != nil {
		fmt.Fprintln(p.diagnostics, "agent candidate proof unavailable")
		return
	}
	if tag == p.lastTag {
		return
	}
	proof, err := p.store.LoadDesired()
	if err != nil || proof == nil || proof.Tag != tag {
		fmt.Fprintln(p.diagnostics, "agent candidate proof unavailable")
		return
	}
	scope := preparation.Scope{Origin: p.binding.Origin, RouterID: p.binding.RouterID, Profile: snapshot.Profile, VerifierID: p.profile}
	if p.application != "" {
		if p.applicationReports && !p.applicationReady() {
			return
		}
		apply := localvalidation.ApplyCurrent
		if p.applicationReports {
			apply = localvalidation.ApplyReportable
		}
		err := apply(ctx, p.application, scope, p.binding.Key, tag)
		if p.reportWake != nil {
			select {
			case p.reportWake <- struct{}{}:
			default:
			}
		}
		if err != nil {
			if ctx.Err() == nil {
				switch {
				case errors.Is(err, restart.ErrRolledBack):
					fmt.Fprintln(p.diagnostics, "agent application failed; previous runtime restored")
				case errors.Is(err, restart.ErrRollbackFailed):
					fmt.Fprintln(p.diagnostics, "agent rollback failed; current runtime unknown")
				case errors.Is(err, restart.ErrReportPending), errors.Is(err, restart.ErrPending), errors.Is(err, restart.ErrDesired), errors.Is(err, restart.ErrBaseline),
					errors.Is(err, preparation.ErrRejected), errors.Is(err, restart.ErrJournal), errors.Is(err, restart.ErrEffects):
					fmt.Fprintln(p.diagnostics, err)
				default:
					fmt.Fprintln(p.diagnostics, "agent controlled application unavailable")
				}
			}
			return
		}
		p.lastTag = tag
		fmt.Fprintln(p.out, "agent revision verified locally")
		return
	}
	client, err := localvalidation.NewClient(p.socket, scope)
	if err != nil {
		fmt.Fprintln(p.diagnostics, "agent local validation unavailable")
		return
	}
	_, err = preparation.New(client, p.store, client).Prepare(ctx, scope, proof)
	switch {
	case ctx.Err() != nil:
		return
	case err == nil:
		p.lastTag = tag
		fmt.Fprintln(p.out, "agent revision prepared")
	case errors.Is(err, preparation.ErrRejected):
		fmt.Fprintln(p.diagnostics, "agent candidate configuration invalid")
	case errors.Is(err, preparation.ErrBaseline):
		fmt.Fprintln(p.diagnostics, "agent working baseline unavailable")
	case errors.Is(err, preparation.ErrState):
		fmt.Fprintln(p.diagnostics, "agent prepared state unavailable")
	case errors.Is(err, preparation.ErrCandidate):
		fmt.Fprintln(p.diagnostics, "agent candidate proof invalid")
	default:
		fmt.Fprintln(p.diagnostics, "agent local validation unavailable")
	}
}
