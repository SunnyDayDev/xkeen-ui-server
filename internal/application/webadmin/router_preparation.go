package webadmin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/google/uuid"
)

var ErrInvalidPreparation = errors.New("invalid router preparation")

var ErrPreparationSourceMissing = errors.New("router preparation source missing")

type PreparationSourceKind string

const (
	PreparationAccepted PreparationSourceKind = "accepted"
	PreparationDraft    PreparationSourceKind = "draft"
)

// PreparationSource is the exact stored snapshot chosen for initial onboarding.
type PreparationSource struct {
	Kind     PreparationSourceKind
	Snapshot *templates.Snapshot
}

type PreparationForm struct {
	RouterID, TemplateID string
	Kind                 PreparationSourceKind
	Version              int64
	Elements             []PreparationElement
}

type PreparationConfirmation struct {
	RouterID, Name, TemplateID string
	Kind                       PreparationSourceKind
	Version                    int64
	Values                     []PersonalInput
}

type PreparedRouter struct {
	RouterID, Name, TemplateID string
	Revision                   int64
}

var ErrPreparedRouterMissing = errors.New("prepared router missing")

type RouterPreparationStore interface {
	ReadSource(context.Context, [32]byte, func() time.Time, string) (PreparationSource, error)
	ReadPrepared(context.Context, [32]byte, func() time.Time, string) (PreparedRouter, error)
	ForPublication([32]byte, func() time.Time) publication.Store
}

type RouterPreparation struct {
	store     RouterPreparationStore
	validator publication.CatalogValidator
	now       func() time.Time
}

func NewRouterPreparation(store RouterPreparationStore, validator publication.CatalogValidator, now func() time.Time) RouterPreparation {
	return RouterPreparation{store: store, validator: validator, now: now}
}

func (s RouterPreparation) Confirm(ctx context.Context, token, proof string, in PreparationConfirmation) (PreparedRouter, error) {
	digest, _, err := SessionProof(token, proof, true)
	if err != nil {
		return PreparedRouter{}, err
	}
	source, err := s.store.ReadSource(ctx, digest, s.now, in.TemplateID)
	if err != nil {
		return PreparedRouter{}, err
	}
	if source.Kind != in.Kind || source.Snapshot.ID != in.TemplateID || source.Snapshot.Version != in.Version {
		return PreparedRouter{}, publication.ErrConflict
	}
	draft, err := prepareRouterSettings(source.Snapshot, in.RouterID, in.Name, in.Values)
	if err != nil {
		return PreparedRouter{}, err
	}
	request := publication.PublishRequest{
		Source: publication.SourceCandidate, Candidate: &draft,
		Expected: publication.Expectations{RouterID: in.RouterID, TemplateID: in.TemplateID},
	}
	if source.Kind == PreparationDraft {
		request.Mode = publication.ConfirmInitial
		request.Expected.TemplateDraftVersion = &in.Version
	} else {
		request.Mode = publication.ConfirmAccepted
		request.Expected.AcceptedVersion = in.Version
	}
	published, err := publication.NewService(s.store.ForPublication(digest, s.now), s.validator).Publish(ctx, request)
	if errors.Is(err, publication.ErrEffectRefused) {
		return PreparedRouter{}, ErrSignedOut
	}
	if err != nil {
		return PreparedRouter{}, err
	}
	return PreparedRouter{RouterID: in.RouterID, Name: in.Name, TemplateID: in.TemplateID, Revision: published.Revision}, nil
}

func (s RouterPreparation) Prepared(ctx context.Context, token, id string) (PreparedRouter, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return PreparedRouter{}, err
	}
	return s.store.ReadPrepared(ctx, digest, s.now, id)
}

func (s RouterPreparation) Describe(ctx context.Context, token, templateID string) (PreparationForm, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return PreparationForm{}, err
	}
	source, err := s.store.ReadSource(ctx, digest, s.now, templateID)
	if err != nil {
		return PreparationForm{}, err
	}
	fields, err := describePreparation(source.Snapshot)
	if err != nil {
		return PreparationForm{}, err
	}
	id, err := newPreparationID()
	if err != nil {
		return PreparationForm{}, err
	}
	return PreparationForm{
		RouterID: id, TemplateID: source.Snapshot.ID, Kind: source.Kind,
		Version: source.Snapshot.Version, Elements: fields,
	}, nil
}

func newPreparationID() (string, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	return id.String(), nil
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

func describePreparation(snapshot *templates.Snapshot) ([]PreparationElement, error) {
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

func prepareRouterSettings(snapshot *templates.Snapshot, id, name string, personal []PersonalInput) (routersettings.Draft, error) {
	if snapshot == nil || !configuration.ValidElementID(id) {
		return routersettings.Draft{}, ErrInvalidPreparation
	}
	if strings.TrimSpace(name) == "" || !utf8.ValidString(name) {
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
