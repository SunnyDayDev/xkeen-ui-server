package publication

import (
	"context"
	"errors"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

var (
	ErrEffectRefused    = errors.New("publication authority is no longer active")
	ErrConflict         = errors.New("publication state changed")
	ErrInvalidState     = errors.New("invalid publication state")
	ErrStoredState      = errors.New("invalid stored publication state")
	ErrVersionExhausted = errors.New("publication version exhausted")
)

// Store reads from one consistent state and commits one prepared router
// publication. Candidate assembly and Xray validation happen outside the store.
type Store interface {
	Read(context.Context, ReadRequest) (*Inputs, error)
	Commit(context.Context, CommitPlan) (int64, error)
}

type ReadRequest struct {
	RouterID string
	// InitialTemplateID selects a source for an unassigned router.
	InitialTemplateID    string
	IncludeTemplateDraft bool
	IncludeRouterDraft   bool
}

type Inputs struct {
	RouterID, TemplateID   string
	Accepted               *templates.Snapshot
	Active                 *ActiveSettings
	Desired                *Revision
	TemplateDraftRequested bool
	TemplateDraft          *templates.Snapshot
	RouterDraftRequested   bool
	RouterDraft            *VersionedRouterDraft
}

type ActiveSettings struct {
	Draft      routersettings.Draft
	OrderBasis *OrderBasis
	Generation int64
}

type VersionedRouterDraft struct {
	Draft      routersettings.Draft
	Generation int64
}

type Revision struct {
	Number  int64
	Catalog VerifiedFiles
}

// VerifiedFiles is the scheduled payload already checked by the publication
// caller. Profile records the Xray profile used for validation.
type VerifiedFiles struct {
	Profile string
	Files   []configuration.CatalogFile
}

type Expectations struct {
	// The token set applies only to this router and selected template.
	RouterID, TemplateID string
	// Zero requires the corresponding accepted/active/desired state to be absent.
	AcceptedVersion, ActiveGeneration, DesiredNumber int64
	// Nil means unused. A pointer to zero requires the included draft to be absent.
	TemplateDraftVersion, RouterDraftGeneration *int64
}

type CommitPlan struct {
	FirstConfirmation     bool
	AcceptInitialTemplate bool
	Expected              Expectations
	Settings              routersettings.Draft
	OrderBasis            *OrderBasis
	// Nil retains an existing desired revision.
	NewRevision *VerifiedFiles
}
