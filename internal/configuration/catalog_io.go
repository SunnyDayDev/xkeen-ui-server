package configuration

// CatalogIOMember identifies an effective entry without exposing its content.
type CatalogIOMember struct {
	ID     string `json:"id"`
	Origin string `json:"origin"`
}

// SelectCatalogIO uses the catalog's selection and shape rules for the two
// collections. Relationship readiness is checked by dependency analysis.
func SelectCatalogIO(input CatalogInput) (inbounds, outbounds []CatalogIOMember, issues []CatalogIssue) {
	groups := catalogGroups(input)
	if input.Inbounds == nil || input.Outbounds == nil {
		return nil, nil, []CatalogIssue{catalogRecordIssue(input.Router, "/inbounds", "missing_catalog_section")}
	}
	if issues := validateCatalogMetadata(input, groups); len(issues) != 0 {
		return nil, nil, issues
	}
	disabled, problems := validateDisabledRefs(input.Router, input.DisabledInbounds, catalogKnownEntries(*input.Inbounds), "duplicate_disabled_inbound")
	if len(problems) != 0 {
		return nil, nil, catalogSelectionIssues(problems)
	}
	inbounds, issues = selectCatalogIOMembers("inbounds", input.Inbounds, disabled)
	if len(issues) != 0 {
		return nil, nil, issues
	}
	outbounds, issues = selectCatalogIOMembers("outbounds", input.Outbounds, nil)
	if len(issues) != 0 {
		return nil, nil, issues
	}
	return inbounds, outbounds, nil
}

func selectCatalogIOMembers(section string, input *RouterSelectionInput, disabled map[string]struct{}) ([]CatalogIOMember, []CatalogIssue) {
	elements, issues := prepareCatalogGroup(catalogGroup{name: section, input: input}, activeCatalogIDs(*input), disabled)
	if len(issues) != 0 {
		return nil, issues
	}
	members := make([]CatalogIOMember, 0, len(elements))
	for _, element := range elements {
		members = append(members, CatalogIOMember{ID: element.id, Origin: element.effective.Origin})
	}
	return members, nil
}
