package configuration

import "unicode/utf8"

// OverrideRef identifies a current override, regardless of mode or filled values.
type OverrideRef struct {
	Source   ElementSource
	Position string
}

// ValidateOverrideSources checks all supplied bindings for one router/template.
// It returns the first issue and never changes inputs. Success does not establish
// source existence, standalone element uniqueness or complete assembly validity.
func ValidateOverrideSources(template, router Owner, overrides []OverrideRef) []ElementIssue {
	if template.Kind != "template" || router.Kind != "router" || !validOwner(template) || !validOwner(router) {
		return invalidContext()
	}
	seen := make(map[string]ElementLocation)
	for _, ref := range overrides {
		location := ElementLocation{Owner: router, Position: ref.Position}
		if !validLocation(location) {
			return invalidContext()
		}
		id := ref.Source.ElementID
		if !ValidElementID(id) {
			id = ""
		}
		code := ""
		switch {
		case ref.Source.TemplateID == "" || !utf8.ValidString(ref.Source.TemplateID):
			code = "invalid_element_record"
		case id == "":
			code = "invalid_element_id"
		case ref.Source.TemplateID != template.ID:
			code = "source_mismatch"
		}
		if code != "" {
			return []ElementIssue{{Code: code, Location: location, ElementID: id, Source: "record"}}
		}
		if first, ok := seen[ref.Source.ElementID]; ok {
			return []ElementIssue{{Code: "duplicate_override", Location: location, OtherLocation: &first, ElementID: ref.Source.ElementID, Source: "record"}}
		}
		seen[ref.Source.ElementID] = location
	}
	return nil
}
