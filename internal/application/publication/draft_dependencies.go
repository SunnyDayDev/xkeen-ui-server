package publication

import (
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type DraftDependencies struct {
	Inbound  *configuration.InboundAnalysis
	Outbound *configuration.OutboundAnalysis
}

// AnalyzeDraftDependencies permits diagnosed unresolved references. Invalid
// structure yields no analysis and cannot authorize a rule state change.
func AnalyzeDraftDependencies(template *templates.Snapshot, draft routersettings.Draft, basis *OrderBasis) (DraftDependencies, error) {
	if template == nil || template.ID != draft.TemplateID || template.Version < 1 {
		return DraftDependencies{}, ErrSourceMismatch
	}
	input, issues := prepareCatalogInput(template, &draft)
	if len(issues) > 0 {
		return DraftDependencies{}, &PreparationError{Issues: issues}
	}
	order, _, issues := preparePersonalOrder(input, template.Version, basis)
	if len(issues) > 0 {
		return DraftDependencies{}, &PreparationError{Issues: issues}
	}
	in := configuration.InboundSnapshot{Rules: *input.Rules, Inbounds: *input.Inbounds, TemplateOrder: input.TemplateOrder, PersonalOrder: order, DisabledRules: input.DisabledRules, DisabledInbounds: input.DisabledInbounds}
	in.OtherShared, in.OtherLocal = otherDraftRefs(input, input.Outbounds, input.Routing)
	incoming, problems := configuration.AnalyzeInboundDependencies(in)
	if len(problems) > 0 {
		return DraftDependencies{}, &PreparationError{Issues: ruleSelectionIssues(problems)}
	}
	out := configuration.OutboundSnapshot{Rules: *input.Rules, Outbounds: *input.Outbounds, Routing: *input.Routing, TemplateOrder: input.TemplateOrder, PersonalOrder: order, DisabledRules: input.DisabledRules}
	out.OtherShared, out.OtherLocal = otherDraftRefs(input, input.Inbounds)
	outgoing, problems := configuration.AnalyzeOutboundDependencies(out)
	if len(problems) > 0 {
		return DraftDependencies{}, &PreparationError{Issues: ruleSelectionIssues(problems)}
	}
	return DraftDependencies{incoming, outgoing}, nil
}

func otherDraftRefs(input configuration.CatalogInput, others ...*configuration.RouterSelectionInput) (shared, local []configuration.ElementRef) {
	others = append(others, input.Log, input.API, input.Stats, input.FakeDNS)
	for i := range input.Extra {
		others = append(others, &input.Extra[i].Selection)
	}
	for _, section := range others {
		if section == nil {
			continue
		}
		for _, record := range section.Shared {
			shared = append(shared, configuration.ElementRef{ID: record.Element.ID, Position: record.Position})
		}
		for _, record := range section.Local {
			local = append(local, configuration.ElementRef{ID: record.ID, Position: record.Position})
		}
	}
	return shared, local
}
