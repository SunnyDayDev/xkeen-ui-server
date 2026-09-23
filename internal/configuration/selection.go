package configuration

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// RouterSelectionInput is a complete snapshot, including inactive elements.
// Positions identify records in that snapshot, not final routing order.
type RouterSelectionInput struct {
	Template, Router Owner
	Shared           []SharedElement
	Local            []LocalContent
	Overrides        []LocatedContentOverride
	Exclusions       []SharedExclusion
}

type SharedElement struct {
	Element  Element
	Position string
}

// LocalContent is literal JSON, without variable definitions or substitution.
type LocalContent struct {
	ID       string
	Content  json.RawMessage
	Position string
}

type LocatedContentOverride struct {
	Override ContentOverride
	Position string
}

type SharedExclusion struct {
	Source   ElementSource
	Position string
}

// RouterSelection is an unordered content set, not a validated Xray config.
type RouterSelection struct{ Elements map[string]EffectiveElement }

// EffectiveElement identifies provenance without assigning final order.
// Source and Mode are absent for local content. Origin is "shared" or "local".
type EffectiveElement struct {
	Content  json.RawMessage
	Origin   string
	Location ElementLocation
	Source   *ElementSource
	Mode     ContentMode
}

// SelectionIssue preserves both assembly details and identity conflict locations.
type SelectionIssue struct {
	Issue
	Location        ElementLocation
	OtherLocation   *ElementLocation
	DefinitionIndex *int
}

// SelectRouterElements validates all identities and bindings, omits excluded
// sources, then selects content. Success owns its buffers, including an empty
// non-nil set; failure returns nil and issues. Inputs are never changed.
func SelectRouterElements(input RouterSelectionInput) (*RouterSelection, []SelectionIssue) {
	if issues := validateSelectionMetadata(input); len(issues) != 0 {
		return nil, issues
	}
	result := &RouterSelection{Elements: make(map[string]EffectiveElement)}
	overrides := make(map[string]LocatedContentOverride)
	for _, override := range input.Overrides {
		overrides[override.Override.Source.ElementID] = override
	}
	excluded := make(map[string]struct{}, len(input.Exclusions))
	for _, exclusion := range input.Exclusions {
		excluded[exclusion.Source.ElementID] = struct{}{}
	}
	for i, shared := range input.Shared {
		if _, found := excluded[shared.Element.ID]; found {
			continue
		}
		location := ElementLocation{Owner: input.Template, Position: shared.Position}
		routerLocation := ElementLocation{Owner: input.Router, Position: "/shared/" + strconv.Itoa(i)}
		var personal *ContentOverride
		if override, found := overrides[shared.Element.ID]; found {
			personal = &override.Override
			routerLocation.Position = override.Position
		}
		content, issues := SelectElementContent(shared.Element, location, routerLocation, personal)
		if len(issues) != 0 {
			converted := make([]SelectionIssue, len(issues))
			for j, issue := range issues {
				converted[j] = SelectionIssue{Issue: issue.Issue, Location: issue.Location}
			}
			return nil, converted
		}
		result.Elements[shared.Element.ID] = EffectiveElement{Content: content.Content, Origin: "shared", Location: location, Source: &content.Source, Mode: content.Mode}
	}
	for _, local := range input.Local {
		if _, issue := parseJSON(local.Content, Issue{ElementID: local.ID, Source: "content"}); issue != nil {
			return nil, []SelectionIssue{{Issue: *issue, Location: ElementLocation{Owner: input.Router, Position: local.Position}}}
		}
		result.Elements[local.ID] = EffectiveElement{Content: bytes.Clone(local.Content), Origin: "local", Location: ElementLocation{Owner: input.Router, Position: local.Position}}
	}
	return result, nil
}

// Validate the entire snapshot before inspecting any active content.
func validateSelectionMetadata(input RouterSelectionInput) []SelectionIssue {
	sharedRefs := make([]ElementRef, len(input.Shared))
	for i, shared := range input.Shared {
		sharedRefs[i] = ElementRef{ID: shared.Element.ID, Position: shared.Position}
	}
	localRefs := make([]ElementRef, len(input.Local))
	for i, local := range input.Local {
		localRefs[i] = ElementRef{ID: local.ID, Position: local.Position}
	}
	if issues := ValidateRouterLocalIDs(input.Template, sharedRefs, input.Router, localRefs); len(issues) != 0 {
		return selectionElementIssues(issues)
	}
	overrideRefs := make([]OverrideRef, len(input.Overrides))
	for i, override := range input.Overrides {
		overrideRefs[i] = OverrideRef{Source: override.Override.Source, Position: override.Position}
	}
	if issues := ValidateOverrideSources(input.Template, input.Router, overrideRefs); len(issues) != 0 {
		return selectionElementIssues(issues)
	}
	// Exclusions use the same source contract, but a distinct duplicate code.
	exclusionRefs := make([]OverrideRef, len(input.Exclusions))
	for i, exclusion := range input.Exclusions {
		exclusionRefs[i] = OverrideRef{Source: exclusion.Source, Position: exclusion.Position}
	}
	if issues := ValidateOverrideSources(input.Template, input.Router, exclusionRefs); len(issues) != 0 {
		for i := range issues {
			if issues[i].Code == "duplicate_override" {
				issues[i].Code = "duplicate_exclusion"
			}
		}
		return selectionElementIssues(issues)
	}
	for _, override := range input.Overrides {
		if override.Override.Mode != ContentModeVariables && override.Override.Mode != ContentModeReplacement {
			return []SelectionIssue{{Issue: Issue{Code: "invalid_element_record", ElementID: override.Override.Source.ElementID, Source: "record"}, Location: ElementLocation{Owner: input.Router, Position: override.Position}}}
		}
	}
	sharedIDs := make(map[string]struct{}, len(input.Shared))
	for _, shared := range input.Shared {
		sharedIDs[shared.Element.ID] = struct{}{}
	}
	for _, refs := range [][]OverrideRef{overrideRefs, exclusionRefs} {
		for _, ref := range refs {
			if _, found := sharedIDs[ref.Source.ElementID]; !found {
				return []SelectionIssue{{Issue: Issue{Code: "source_not_found", ElementID: ref.Source.ElementID, Source: "record"}, Location: ElementLocation{Owner: input.Router, Position: ref.Position}}}
			}
		}
	}
	return nil
}

func selectionElementIssues(issues []ElementIssue) []SelectionIssue {
	result := make([]SelectionIssue, len(issues))
	for i, issue := range issues {
		result[i] = SelectionIssue{Issue: Issue{Code: issue.Code, ElementID: issue.ElementID, Source: issue.Source, Path: issue.Path, Offset: issue.Offset}, Location: issue.Location, OtherLocation: issue.OtherLocation, DefinitionIndex: issue.DefinitionIndex}
	}
	return result
}
