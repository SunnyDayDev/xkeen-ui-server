package webadmin

import (
	"context"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/jsonvalue"
)

type RouterVariableEditor struct {
	store RouterDraftStore
	now   func() time.Time
}

func NewRouterVariableEditor(store RouterDraftStore, now func() time.Time) RouterVariableEditor {
	return RouterVariableEditor{store: store, now: now}
}

type RouterVariables struct {
	Basis     RouterDraftBasis
	Name      string
	Sections  []RouterVariableSection
	Selection *RouterVariableSelection
}

type RouterVariableSection struct {
	Name    string                 `json:"name"`
	Members []RouterVariableMember `json:"members"`
}

type RouterVariableMember struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type RouterVariableSelection = routerdraft.VariableSelection
type RouterVariableValue = routerdraft.VariableValue

func (e RouterVariableEditor) Read(ctx context.Context, token, routerID, elementID string) (RouterVariables, error) {
	digest, _, err := SessionProof(token, "", false)
	if err != nil {
		return RouterVariables{}, err
	}
	snapshot, err := e.store.Read(ctx, digest, e.now, routerID)
	if err != nil {
		return RouterVariables{}, err
	}
	return readRouterVariables(snapshot, elementID)
}

func (e RouterVariableEditor) Save(ctx context.Context, token, csrf string, basis RouterDraftBasis, elementID string, changes []routersettings.VariableChange) (RouterVariables, error) {
	digest, _, err := SessionProof(token, csrf, true)
	if err != nil {
		return RouterVariables{}, err
	}
	snapshot, err := e.store.Read(ctx, digest, e.now, basis.RouterID)
	if err != nil {
		return RouterVariables{}, err
	}
	if snapshot.Basis != basis {
		return RouterVariables{}, ErrRouterDraftConflict
	}
	snapshot.Editable, err = routerdraft.EditVariables(snapshot, elementID, changes)
	if err != nil {
		return RouterVariables{}, err
	}
	result, err := readRouterVariables(snapshot, elementID)
	if err != nil {
		return RouterVariables{}, err
	}
	generation, err := e.store.Write(ctx, digest, e.now, basis, snapshot.Editable)
	if err != nil {
		return RouterVariables{}, err
	}
	result.Basis.DraftGeneration = generation
	return result, nil
}

func sharedRouterElement(snapshot RouterDraftSnapshot, elementID string) (configuration.Element, string, error) {
	return routerdraft.SharedElement(snapshot, elementID)
}

func readRouterVariables(snapshot RouterDraftSnapshot, elementID string) (RouterVariables, error) {
	if snapshot.Editable.TemplateID != snapshot.Basis.TemplateID {
		return RouterVariables{}, ErrRouterDraftForeign
	}
	result := RouterVariables{Basis: snapshot.Basis, Name: snapshot.Name, Sections: make([]RouterVariableSection, 0, len(snapshot.Accepted.Sections))}
	for _, section := range snapshot.Accepted.Sections {
		if len(section.Elements) == 0 {
			continue
		}
		group := RouterVariableSection{Name: section.Name, Members: make([]RouterVariableMember, 0, len(section.Elements))}
		for _, item := range section.Elements {
			element := item.Element
			label := element.ID
			if tree, issue := jsonvalue.Parse([]byte(element.Content)); issue == nil {
				if object, ok := tree.(map[string]any); ok {
					if tag, ok := object["tag"].(string); ok && tag != "" {
						label = tag
					}
				}
			}
			group.Members = append(group.Members, RouterVariableMember{ID: element.ID, Label: label})
		}
		result.Sections = append(result.Sections, group)
	}
	if elementID == "" {
		return result, nil
	}
	selection, err := routerdraft.InspectVariables(snapshot, elementID)
	if err != nil {
		return RouterVariables{}, err
	}
	result.Selection = &selection
	return result, nil
}
