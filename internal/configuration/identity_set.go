package configuration

import "unicode/utf8"

// ElementRef describes a standalone element, including inactive/excluded ones.
// Overrides must not be supplied as standalone local elements.
type ElementRef struct{ ID, Position string }

func ValidateTemplateIDs(owner Owner, elements []ElementRef) []ElementIssue {
	if owner.Kind != "template" || !validOwner(owner) {
		return invalidContext()
	}
	return validateIDSet(owner, elements, make(map[string]ElementLocation))
}

func validateIDSet(owner Owner, elements []ElementRef, seen map[string]ElementLocation) []ElementIssue {
	for _, element := range elements {
		location := ElementLocation{Owner: owner, Position: element.Position}
		if !validLocation(location) {
			return invalidContext()
		}
		if !ValidElementID(element.ID) {
			return []ElementIssue{{Code: "invalid_element_id", Location: location, Source: "record"}}
		}
		if first, found := seen[element.ID]; found {
			return []ElementIssue{{Code: "duplicate_element_id", Location: location, OtherLocation: &first, ElementID: element.ID, Source: "record"}}
		}
		seen[element.ID] = location
	}
	return nil
}

func ValidateRouterLocalIDs(template Owner, shared []ElementRef, router Owner, local []ElementRef) []ElementIssue {
	if template.Kind != "template" || router.Kind != "router" || !validOwner(template) || !validOwner(router) {
		return invalidContext()
	}
	seen := make(map[string]ElementLocation)
	if issues := validateIDSet(template, shared, seen); len(issues) != 0 {
		return issues
	}
	return validateIDSet(router, local, seen)
}

func validOwner(owner Owner) bool {
	return (owner.Kind == "template" || owner.Kind == "router") && owner.ID != "" && utf8.ValidString(owner.ID)
}

func validLocation(location ElementLocation) bool {
	p := location.Position
	if !validOwner(location.Owner) || !utf8.ValidString(p) || (p != "" && p[0] != '/') {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] == '~' {
			i++
			if i >= len(p) || (p[i] != '0' && p[i] != '1') {
				return false
			}
		}
	}
	return true
}

func invalidContext() []ElementIssue {
	return []ElementIssue{{Code: "invalid_element_record", Source: "record"}}
}
