package publication

import (
	"context"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

// TemplateGroupStore reads and commits one complete template publication group.
type TemplateGroupStore interface {
	ReadTemplateGroup(context.Context, string) (*TemplateGroupInputs, error)
	CommitTemplateGroup(context.Context, TemplateGroupPlan) (*TemplateGroupResult, error)
}

type TemplateGroupInputs struct {
	Exclusions    *configuration.ExclusionSource
	TemplateDraft *templates.Snapshot
	Accepted      *templates.Snapshot
	Members       []TemplateGroupMember
}

type TemplateGroupMember struct {
	Active  ActiveSettings
	Desired Revision
}

// GroupRouterExpectation identifies one member and the state used to assemble its files.
type GroupRouterExpectation struct {
	RouterID                        string
	ActiveGeneration, DesiredNumber int64
}

type GroupRouterPlan struct {
	Expected      GroupRouterExpectation
	PersonalOrder []string
	OrderBasis    *OrderBasis
	// Nil retains the existing desired revision.
	NewRevision *VerifiedFiles
}

type TemplateGroupPlan struct {
	TemplateID                            string
	TemplateDraftVersion, AcceptedVersion int64
	// Nil accepts the existing draft without incrementing its version.
	Edit    *configuration.TemplateDraft
	Members []GroupRouterPlan
}

type TemplateGroupResult struct {
	TemplateID string
	Version    int64
	Routers    []GroupRouterResult
}

type GroupRouterResult struct {
	RouterID string
	Revision int64
	Changed  bool
}
