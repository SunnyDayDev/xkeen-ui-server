package routersettings

import "github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"

type ReplacementSourceError struct{ Issue configuration.Issue }

func (*ReplacementSourceError) Error() string { return "invalid replacement JSON" }

// EditReplacement delegates mode transitions to B05 and changes only the
// selected override. The router's exclusions and full rule order stay intact.
func EditReplacement(base Draft, section string, source configuration.ElementSource, action configuration.ContentAction) (Draft, error) {
	current, err := SharedOverride(base, section, source)
	if err != nil {
		return Draft{}, err
	}
	if action.Kind == configuration.ReturnToVariables && (current == nil || current.Mode != configuration.ContentModeReplacement) {
		return Draft{}, ErrVariableMode
	}
	if action.Kind == configuration.ResetContentOverride && current == nil {
		return Draft{}, ErrInvalidDraft
	}
	if action.Kind != configuration.ActivateReplacement && action.Content != nil {
		return Draft{}, ErrInvalidDraft
	}
	templateLocation := configuration.ElementLocation{Owner: configuration.Owner{Kind: "template", ID: source.TemplateID}, Position: "/" + section}
	routerLocation := configuration.ElementLocation{Owner: configuration.Owner{Kind: "router", ID: base.ID}, Position: "/" + section}
	next, issues := configuration.TransitionContentOverride(source.ElementID, templateLocation, routerLocation, current, action)
	if len(issues) != 0 {
		if issue := issues[0].Issue; issue.Source == "content" {
			return Draft{}, &ReplacementSourceError{Issue: issue}
		}
		return Draft{}, ErrInvalidDraft
	}
	return withSharedOverride(base, section, source, next), nil
}
