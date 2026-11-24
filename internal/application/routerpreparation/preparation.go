package routerpreparation

import (
	"encoding/json"
	"errors"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

var ErrInvalidPreparation = errors.New("invalid router preparation")

type SourceKind string

const (
	Accepted SourceKind = "accepted"
	Draft    SourceKind = "draft"
)

// Source is the exact stored snapshot chosen for initial onboarding.
type Source struct {
	Kind     SourceKind
	Snapshot *templates.Snapshot
}

func BuildPublication(source Source, id, name string, personal []PersonalInput) (publication.PublishRequest, error) {
	draft, err := BuildDraft(source.Snapshot, id, name, personal)
	if err != nil {
		return publication.PublishRequest{}, err
	}
	request := publication.PublishRequest{
		Source: publication.SourceCandidate, Candidate: &draft,
		Expected: publication.Expectations{RouterID: id, TemplateID: source.Snapshot.ID},
	}
	switch source.Kind {
	case Draft:
		request.Mode = publication.ConfirmInitial
		version := source.Snapshot.Version
		request.Expected.TemplateDraftVersion = &version
	case Accepted:
		request.Mode = publication.ConfirmAccepted
		request.Expected.AcceptedVersion = source.Snapshot.Version
	default:
		return publication.PublishRequest{}, ErrInvalidPreparation
	}
	return request, nil
}

// PreparationInputError identifies a field without including authored values.
type PreparationInputError struct {
	Code, ElementID, Variable string
}

func (e *PreparationInputError) Error() string { return ErrInvalidPreparation.Error() }
func (e *PreparationInputError) Unwrap() error { return ErrInvalidPreparation }

type PreparationElement struct {
	Inactive           bool
	Section, ElementID string
	Variables          []PreparationVariable
}

type PreparationVariable struct {
	Name, Type, DefaultSource string
	Required                  bool
}

type PersonalInput struct {
	ElementID, Name, Source string
}

func Describe(snapshot *templates.Snapshot) ([]PreparationElement, error) {
	if snapshot == nil {
		return nil, ErrInvalidPreparation
	}
	result := make([]PreparationElement, 0)
	for _, section := range snapshot.Sections {
		for _, item := range section.Elements {
			element := item.Element
			if len(element.Variables) == 0 {
				continue
			}
			inactive := item.Disabled && publication.SupportsTemplateDisabling(section.Name)
			group := PreparationElement{Section: section.Name, ElementID: element.ID, Inactive: inactive, Variables: make([]PreparationVariable, 0, len(element.Variables))}
			names := make(map[string]bool, len(element.Variables))
			for _, definition := range element.Variables {
				variable := PreparationVariable{Name: definition.Name, Type: definition.Type, DefaultSource: string(definition.Default)}
				if inactive {
					group.Variables = append(group.Variables, variable)
					continue
				}
				if names[definition.Name] {
					return nil, &PreparationInputError{Code: "duplicate_variable", ElementID: element.ID, Variable: definition.Name}
				}
				names[definition.Name] = true
				_, issues := configuration.PrepareValues(element.ID, []configuration.VariableDefinition{definition}, nil)
				if len(issues) != 0 {
					if issues[0].Code != "missing_value" {
						return nil, preparationValueError(issues[0])
					}
					variable.Required = true
				}
				group.Variables = append(group.Variables, variable)
			}
			result = append(result, group)
		}
	}
	return result, nil
}

func BuildDraft(snapshot *templates.Snapshot, id, name string, personal []PersonalInput) (routersettings.Draft, error) {
	if snapshot == nil || !configuration.ValidElementID(id) {
		return routersettings.Draft{}, ErrInvalidPreparation
	}
	if !routersettings.ValidDisplayName(name) {
		return routersettings.Draft{}, &PreparationInputError{Code: "router_name_required"}
	}
	values := make(map[string]map[string]json.RawMessage)
	for _, item := range personal {
		assigned := values[item.ElementID]
		if assigned == nil {
			assigned = make(map[string]json.RawMessage)
			values[item.ElementID] = assigned
		}
		if _, repeated := assigned[item.Name]; repeated {
			return routersettings.Draft{}, &PreparationInputError{Code: "duplicate_value"}
		}
		assigned[item.Name] = json.RawMessage(item.Source)
	}
	draft := routersettings.Draft{ID: id, Name: name, TemplateID: snapshot.ID}
	for _, section := range snapshot.Sections {
		group := routersettings.Group{Name: section.Name}
		for _, item := range section.Elements {
			element := item.Element
			assigned := values[element.ID]
			delete(values, element.ID)
			if item.Disabled && publication.SupportsTemplateDisabling(section.Name) {
				if len(assigned) != 0 {
					return routersettings.Draft{}, &PreparationInputError{Code: "inactive_element", ElementID: element.ID}
				}
				continue
			}
			if _, issues := configuration.PrepareValues(element.ID, element.Variables, assigned); len(issues) != 0 {
				return routersettings.Draft{}, preparationValueError(issues[0])
			}
			if len(assigned) == 0 {
				continue
			}
			group.Overrides = append(group.Overrides, configuration.ContentOverride{
				Source: configuration.ElementSource{TemplateID: snapshot.ID, ElementID: element.ID},
				Mode:   configuration.ContentModeVariables, Values: assigned,
			})
		}
		if len(group.Overrides) != 0 {
			draft.Groups = append(draft.Groups, group)
		}
	}
	if len(values) != 0 {
		return routersettings.Draft{}, &PreparationInputError{Code: "unknown_element"}
	}
	return draft, nil
}

func preparationValueError(issue configuration.Issue) error {
	loc := &PreparationInputError{Code: issue.Code, ElementID: issue.ElementID}
	if issue.Code != "unknown_variable" && issue.VariableName != nil {
		loc.Variable = *issue.VariableName
	}
	return loc
}
