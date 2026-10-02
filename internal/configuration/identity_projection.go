package configuration

import (
	"bytes"
	"encoding/json"
)

const XrayIdentityVersion = "26.3.27"

// IdentityProjection describes a ready value at an explicitly chosen Xray position.
type IdentityProjection struct {
	ElementID string
	Location  ElementLocation
	Position  string
	Version   string
	Content   json.RawMessage
}

// ProjectElementIdentity marks only a supported root object, without changing input.
func ProjectElementIdentity(input IdentityProjection) (json.RawMessage, []SelectionIssue) {
	if !validLocation(input.Location) {
		return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_element_record", Source: "record"}}}
	}
	if !ValidElementID(input.ElementID) {
		return nil, []SelectionIssue{{Issue: Issue{Code: "invalid_element_id", Source: "record"}, Location: input.Location}}
	}
	value, issue := parseJSON(input.Content, Issue{ElementID: input.ElementID, Source: "content"})
	if issue != nil {
		return nil, []SelectionIssue{{Issue: *issue, Location: input.Location}}
	}
	if object, ok := value.(map[string]any); ok && input.Version == XrayIdentityVersion && identityPositionSupported(input.Position) {
		if marker, present := object["_xkeenUiId"]; present && marker != input.ElementID {
			path := "/_xkeenUiId"
			return nil, []SelectionIssue{{Issue: Issue{Code: "identity_marker_conflict", ElementID: input.ElementID, Source: "content", Path: &path}, Location: input.Location}}
		}
		object["_xkeenUiId"] = input.ElementID
		result, _ := json.Marshal(object)
		return result, nil
	}
	return bytes.Clone(input.Content), nil
}

func identityPositionSupported(position string) bool {
	switch position {
	case "inbound", "outbound", "rule", "fakedns", "log", "dns", "routing", "policy", "stats", "observatory":
		return true
	}
	return false
}
