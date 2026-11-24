// Package publication coordinates inputs and results of revision publication.
package publication

import (
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

// OrderBasis describes the complete shared order used to compose a personal order.
type OrderBasis struct {
	TemplateID      string
	TemplateVersion int64
	SharedRuleIDs   []string
}

// PreparedRouterCandidate owns the assembled files and proposed personal order.
type PreparedRouterCandidate struct {
	RouterID        string
	TemplateID      string
	TemplateVersion int64
	Catalog         *configuration.XrayCatalog
	PersonalOrder   []string
	OrderBasis      *OrderBasis
}

// PrepareRouterCandidate assembles one selected set of snapshots without I/O.
func PrepareRouterCandidate(template *templates.Snapshot, router *routersettings.Draft, basis *OrderBasis) (*PreparedRouterCandidate, []configuration.CatalogIssue) {
	if template == nil || router == nil {
		return nil, candidateFailure("missing_publication_snapshot", configuration.Owner{}, "")
	}
	if template.Version < 1 || !validIdentity(template.ID) || !validIdentity(router.ID) || router.TemplateID != template.ID {
		return nil, candidateFailure("invalid_publication_context", configuration.Owner{}, "")
	}
	input, issues := prepareCatalogInput(template, router)
	if len(issues) != 0 {
		return nil, issues
	}
	personalOrder, nextBasis, issues := preparePersonalOrder(input, template.Version, basis)
	if len(issues) != 0 {
		return nil, issues
	}
	input.PersonalOrder = personalOrder
	catalog, issues := configuration.AssembleXrayCatalog(input)
	if len(issues) != 0 {
		return nil, issues
	}
	return &PreparedRouterCandidate{
		RouterID: router.ID, TemplateID: template.ID, TemplateVersion: template.Version, Catalog: catalog,
		PersonalOrder: personalOrder, OrderBasis: nextBasis,
	}, nil
}

// SupportsTemplateDisabling reports the sections whose disabled elements
// are excluded from the effective catalog.
func SupportsTemplateDisabling(section string) bool {
	return section == "rules" || section == "inbounds"
}

func prepareCatalogInput(template *templates.Snapshot, router *routersettings.Draft) (configuration.CatalogInput, []configuration.CatalogIssue) {
	input := configuration.CatalogInput{
		Template:      configuration.Owner{Kind: "template", ID: template.ID},
		Router:        configuration.Owner{Kind: "router", ID: router.ID},
		PersonalOrder: router.PersonalOrder,
	}
	selections := make(map[string]*configuration.RouterSelectionInput)
	var names []string
	group := func(name string) *configuration.RouterSelectionInput {
		if existing := selections[name]; existing != nil {
			return existing
		}
		created := &configuration.RouterSelectionInput{Template: input.Template, Router: input.Router}
		selections[name] = created
		names = append(names, name)
		return created
	}

	templateNames := make(map[string]string, len(template.Sections))
	for i, section := range template.Sections {
		position := "/sections/" + strconv.Itoa(i)
		if issues := checkSectionName(section.Name, input.Template, position, templateNames); len(issues) != 0 {
			return configuration.CatalogInput{}, issues
		}
		target := group(section.Name)
		for j, item := range section.Elements {
			location := template.ElementLocation(i, j)
			target.Shared = append(target.Shared, configuration.SharedElement{Element: item.Element, Position: location.Position})
			if !item.Disabled {
				continue
			}
			ref := configuration.ElementRef{ID: item.Element.ID, Position: location.Position}
			if !SupportsTemplateDisabling(section.Name) {
				return configuration.CatalogInput{}, candidateFailure("unsupported_disabled_section", location.Owner, location.Position)
			}
			if section.Name == "rules" {
				input.DisabledRules = append(input.DisabledRules, ref)
			} else {
				input.DisabledInbounds = append(input.DisabledInbounds, ref)
			}
		}
	}

	routerNames := make(map[string]string, len(router.Groups))
	for i, section := range router.Groups {
		position := "/groups/" + strconv.Itoa(i)
		if issues := checkSectionName(section.Name, input.Router, position, routerNames); len(issues) != 0 {
			return configuration.CatalogInput{}, issues
		}
		target := group(section.Name)
		for j, item := range section.Local {
			target.Local = append(target.Local, configuration.LocalContent{
				ID: item.ID, Content: item.Content, Position: position + "/local/" + strconv.Itoa(j),
			})
		}
		for j, override := range section.Overrides {
			target.Overrides = append(target.Overrides, configuration.LocatedContentOverride{
				Override: override, Position: position + "/overrides/" + strconv.Itoa(j),
			})
		}
		for j, exclusion := range section.Exclusions {
			target.Exclusions = append(target.Exclusions, configuration.SharedExclusion{
				Source: exclusion, Position: position + "/exclusions/" + strconv.Itoa(j),
			})
		}
	}

	input.Log, input.API = group("log"), group("api")
	input.Inbounds, input.Outbounds = group("inbounds"), group("outbounds")
	input.Routing, input.Rules = group("routing"), group("rules")
	input.Stats, input.FakeDNS = selections["stats"], selections["fakedns"]
	for _, name := range names {
		switch name {
		case "log", "api", "inbounds", "outbounds", "routing", "rules", "stats", "fakedns":
			continue
		}
		input.Extra = append(input.Extra, configuration.CatalogSectionInput{Name: name, Selection: *selections[name]})
	}
	input.TemplateOrder = make([]string, len(input.Rules.Shared))
	for i, rule := range input.Rules.Shared {
		input.TemplateOrder[i] = rule.Element.ID
	}
	input.DisabledRules = mergeDisabled(input.DisabledRules, router.DisabledRules, "/disabledRules")
	input.DisabledInbounds = mergeDisabled(input.DisabledInbounds, router.DisabledInbounds, "/disabledInbounds")
	return input, nil
}

func preparePersonalOrder(input configuration.CatalogInput, version int64, basis *OrderBasis) ([]string, *OrderBasis, []configuration.CatalogIssue) {
	if input.PersonalOrder == nil {
		return nil, nil, nil
	}
	order := configuration.RuleOrderInput{
		Template: input.Template, Router: input.Router, TemplateOrder: input.TemplateOrder, PersonalOrder: input.PersonalOrder,
		Shared: make([]configuration.ElementRef, len(input.Rules.Shared)),
		Local:  make([]configuration.ElementRef, len(input.Rules.Local)),
	}
	for i, rule := range input.Rules.Shared {
		order.Shared[i] = configuration.ElementRef{ID: rule.Element.ID, Position: rule.Position}
	}
	for i, local := range input.Rules.Local {
		order.Local[i] = configuration.ElementRef{ID: local.ID, Position: local.Position}
	}
	return alignRuleOrder(order, version, basis)
}

func alignRuleOrder(input configuration.RuleOrderInput, version int64, basis *OrderBasis) ([]string, *OrderBasis, []configuration.CatalogIssue) {
	if input.PersonalOrder == nil {
		return nil, nil, nil
	}
	if basis == nil || basis.SharedRuleIDs == nil {
		return nil, nil, candidateFailure("order_basis_required", input.Router, "/orderBasis")
	}
	if basis.TemplateID != input.Template.ID || basis.TemplateVersion < 1 || basis.TemplateVersion > version {
		return nil, nil, candidateFailure("order_basis_mismatch", input.Router, "/orderBasis")
	}
	nextBasis := &OrderBasis{
		TemplateID: input.Template.ID, TemplateVersion: version, SharedRuleIDs: slices.Clone(input.TemplateOrder),
	}
	if basis.TemplateVersion == version {
		if !slices.Equal(basis.SharedRuleIDs, input.TemplateOrder) {
			return nil, nil, candidateFailure("order_basis_mismatch", input.Router, "/orderBasis/sharedRuleIDs")
		}
		return slices.Clone(input.PersonalOrder), nextBasis, nil
	}

	// Reconcile identities before the current exclusions and disables are applied.
	previous := configuration.RuleOrderInput{
		Template: input.Template, Router: input.Router,
		TemplateOrder: basis.SharedRuleIDs, PersonalOrder: input.PersonalOrder,
	}
	for i, id := range basis.SharedRuleIDs {
		previous.Shared = append(previous.Shared, configuration.ElementRef{
			ID: id, Position: "/orderBasis/sharedRuleIDs/" + strconv.Itoa(i),
		})
	}
	previous.Local = input.Local
	order, issues := configuration.ReconcileRuleOrder(previous, input.Shared, input.TemplateOrder)
	if len(issues) != 0 {
		result := make([]configuration.CatalogIssue, len(issues))
		for i, issue := range issues {
			result[i] = configuration.CatalogIssue{SelectionIssue: issue}
		}
		return nil, nil, result
	}
	return order.PersonalOrder, nextBasis, nil
}

func mergeDisabled(shared []configuration.ElementRef, personal []string, position string) []configuration.ElementRef {
	refs := make([]configuration.ElementRef, 0, len(shared)+len(personal))
	personalIDs := make(map[string]struct{}, len(personal))
	for i, id := range personal {
		personalIDs[id] = struct{}{}
		refs = append(refs, configuration.ElementRef{ID: id, Position: position + "/" + strconv.Itoa(i)})
	}
	for _, ref := range shared {
		if _, exists := personalIDs[ref.ID]; !exists {
			refs = append(refs, ref)
		}
	}
	return refs
}

func checkSectionName(name string, owner configuration.Owner, position string, seen map[string]string) []configuration.CatalogIssue {
	if !validIdentity(name) {
		return candidateFailure("invalid_catalog_record", owner, position)
	}
	if first, exists := seen[name]; exists {
		issues := candidateFailure("duplicate_catalog_section", owner, position)
		issues[0].OtherLocation = &configuration.ElementLocation{Owner: owner, Position: first}
		return issues
	}
	seen[name] = position
	return nil
}

func validIdentity(id string) bool { return id != "" && utf8.ValidString(id) }

func candidateFailure(code string, owner configuration.Owner, position string) []configuration.CatalogIssue {
	return []configuration.CatalogIssue{{SelectionIssue: configuration.SelectionIssue{
		Issue:    configuration.Issue{Code: code, Source: "record"},
		Location: configuration.ElementLocation{Owner: owner, Position: position},
	}}}
}
