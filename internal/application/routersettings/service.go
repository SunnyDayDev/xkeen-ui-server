// Package routersettings stores editable snapshots independently of assembly.
package routersettings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/jsonvalue"
)

var (
	ErrInvalidDraft = errors.New("invalid router draft")
	ErrStoredDraft  = errors.New("invalid stored router draft")
)

type Draft struct {
	Exclusion            *ExclusionOverride
	ID, Name, TemplateID string
	Groups               []Group
	// Nil inherits template order. An empty non-nil slice is explicit.
	PersonalOrder                   []string
	DisabledRules, DisabledInbounds []string
}

type Group struct {
	Name       string
	Local      []LocalElement
	Overrides  []configuration.ContentOverride
	Exclusions []configuration.ElementSource
}

type LocalElement struct {
	ID      string
	Content json.RawMessage
}

// Repository persists complete snapshots. Save does not retain or modify the
// input buffers. Get transfers a complete, independent snapshot to the caller.
type Repository interface {
	Save(ctx context.Context, draft Draft) error
	Get(ctx context.Context, id string) (Draft, bool, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) Save(ctx context.Context, draft Draft) error {
	if err := ValidateDraft(draft); err != nil {
		return err
	}
	return s.repository.Save(ctx, draft)
}

func (s *Service) Get(ctx context.Context, id string) (Draft, bool, error) {
	if !validID(id) {
		return Draft{}, false, invalid("/id", "invalid identity")
	}
	draft, found, err := s.repository.Get(ctx, id)
	if err != nil || !found {
		return Draft{}, false, err
	}
	if draft.ID != id || ValidateDraft(draft) != nil {
		return Draft{}, false, ErrStoredDraft
	}
	return draft, true, nil
}

func validID(id string) bool {
	return id != "" && utf8.ValidString(id)
}

func invalid(position, reason string) error {
	return fmt.Errorf("%w at %s: %s", ErrInvalidDraft, position, reason)
}

// ValidateDraft checks the editable snapshot independently of SQLite and assembly.
func ValidateDraft(draft Draft) error {
	if !draft.Exclusion.valid(draft) {
		return invalid("/exclusion", "invalid source or personal settings")
	}
	if !validID(draft.ID) {
		return invalid("/id", "invalid identity")
	}
	if !validID(draft.TemplateID) {
		return invalid("/templateId", "invalid identity")
	}
	if !utf8.ValidString(draft.Name) {
		return invalid("/name", "invalid text")
	}
	template := configuration.Owner{Kind: "template", ID: draft.TemplateID}
	router := configuration.Owner{Kind: "router", ID: draft.ID}
	groups := make(map[string]struct{}, len(draft.Groups))
	var localRefs []configuration.ElementRef
	var overrideRefs, exclusionRefs []configuration.OverrideRef
	for i, group := range draft.Groups {
		position := "/groups/" + strconv.Itoa(i)
		if !validID(group.Name) {
			return invalid(position, "invalid section name")
		}
		if _, exists := groups[group.Name]; exists {
			return invalid(position, "duplicate section name")
		}
		groups[group.Name] = struct{}{}
		for j, local := range group.Local {
			record := position + "/local/" + strconv.Itoa(j)
			localRefs = append(localRefs, configuration.ElementRef{ID: local.ID, Position: record})
			if _, issue := jsonvalue.Parse(local.Content); issue != nil {
				return invalid(record, "invalid local JSON")
			}
		}
		for j, override := range group.Overrides {
			record := position + "/overrides/" + strconv.Itoa(j)
			overrideRefs = append(overrideRefs, configuration.OverrideRef{Source: override.Source, Position: record})
			if err := validateOverride(record, override); err != nil {
				return err
			}
		}
		for j, source := range group.Exclusions {
			record := position + "/exclusions/" + strconv.Itoa(j)
			exclusionRefs = append(exclusionRefs, configuration.OverrideRef{Source: source, Position: record})
		}
	}
	if issues := configuration.ValidateRouterLocalIDs(template, nil, router, localRefs); len(issues) != 0 {
		return invalid(issues[0].Location.Position, issues[0].Code)
	}
	if issues := configuration.ValidateOverrideSources(template, router, overrideRefs); len(issues) != 0 {
		return invalid(issues[0].Location.Position, issues[0].Code)
	}
	if issues := configuration.ValidateOverrideSources(template, router, exclusionRefs); len(issues) != 0 {
		code := issues[0].Code
		if code == "duplicate_override" {
			code = "duplicate_exclusion"
		}
		return invalid(issues[0].Location.Position, code)
	}
	for _, list := range []struct {
		position string
		ids      []string
	}{
		{"/personalOrder", draft.PersonalOrder},
		{"/disabledRules", draft.DisabledRules},
		{"/disabledInbounds", draft.DisabledInbounds},
	} {
		seen := make(map[string]struct{}, len(list.ids))
		for i, id := range list.ids {
			position := list.position + "/" + strconv.Itoa(i)
			if !configuration.ValidElementID(id) {
				return invalid(position, "invalid element UUID")
			}
			if _, exists := seen[id]; exists {
				return invalid(position, "duplicate element UUID")
			}
			seen[id] = struct{}{}
		}
	}
	return nil
}

func validateOverride(position string, override configuration.ContentOverride) error {
	switch override.Mode {
	case configuration.ContentModeVariables:
		if override.Content != nil {
			return invalid(position, "unexpected replacement content")
		}
	case configuration.ContentModeReplacement:
		if _, issue := jsonvalue.Parse(override.Content); issue != nil {
			return invalid(position, "invalid replacement JSON")
		}
	default:
		return invalid(position, "invalid override mode")
	}
	for name, value := range override.Values {
		if !utf8.ValidString(name) {
			return invalid(position, "invalid variable name encoding")
		}
		if _, issue := jsonvalue.Parse(value); issue != nil {
			return invalid(position, "invalid variable JSON")
		}
	}
	return nil
}
