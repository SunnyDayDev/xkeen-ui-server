package templates

import (
	"context"
	"errors"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type memoryRepository map[string]configuration.TemplateDraft

func (m memoryRepository) Insert(_ context.Context, draft configuration.TemplateDraft) error {
	if _, exists := m[draft.ID]; exists {
		return ErrConflict
	}
	m[draft.ID] = draft
	return nil
}

func (m memoryRepository) Find(_ context.Context, id string) (*configuration.TemplateDraft, error) {
	draft, exists := m[id]
	if !exists {
		return nil, ErrNotFound
	}
	return &draft, nil
}

func TestCreateAndGet(t *testing.T) {
	s := New(memoryRepository{})
	for _, id := range []string{"demo", "Demo", " demo ", "demo\x00"} {
		if err := s.Create(t.Context(), configuration.TemplateDraft{ID: id}); err != nil {
			t.Errorf("create %q: %v", id, err)
			continue
		}
		got, err := s.Get(t.Context(), id)
		if err != nil || got == nil || got.ID != id || len(got.Sections) != 0 {
			t.Errorf("get %q: %#v, %v", id, got, err)
		}
	}
	if err := s.Create(t.Context(), configuration.TemplateDraft{ID: "demo"}); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate create: %v", err)
	}
	if got, err := s.Get(t.Context(), "unknown"); got != nil || !errors.Is(err, ErrNotFound) {
		t.Errorf("get unknown: %#v, %v", got, err)
	}
	for _, invalid := range []string{"", "\xff"} {
		if err := s.Create(t.Context(), configuration.TemplateDraft{ID: invalid}); !errors.Is(err, ErrInvalid) {
			t.Errorf("create invalid: %v", err)
		}
		if got, err := s.Get(t.Context(), invalid); got != nil || !errors.Is(err, ErrInvalid) {
			t.Errorf("get invalid: %#v, %v", got, err)
		}
	}
}

func TestCreateValidatesCompleteDraft(t *testing.T) {
	s := New(memoryRepository{})
	element := configuration.TemplateElement{
		Element: configuration.Element{
			ID:      "550e8400-e29b-41d4-a716-446655440000",
			Content: " { \"port\": \"${port}\", \"optional\":null }\n",
		},
	}
	first := configuration.TemplateSection{Name: "inbounds", Elements: []configuration.TemplateElement{element}}
	element.Disabled = true
	second := configuration.TemplateSection{Name: "rules", Elements: []configuration.TemplateElement{element}}
	invalid := configuration.TemplateDraft{ID: "duplicate", Sections: []configuration.TemplateSection{first, second}}
	err := s.Create(t.Context(), invalid)
	var draftError *InvalidDraftError
	if !errors.As(err, &draftError) || len(draftError.Issues) != 1 ||
		draftError.Issues[0].Code != "duplicate_element_id" ||
		draftError.Issues[0].Location.Position != "/sections/1/elements/0" ||
		draftError.Issues[0].OtherLocation == nil || draftError.Issues[0].OtherLocation.Position != "/sections/0/elements/0" {
		t.Errorf("duplicate diagnostic: %#v", err)
	}
	if got, err := s.Get(t.Context(), invalid.ID); got != nil || !errors.Is(err, ErrNotFound) {
		t.Errorf("invalid draft persisted: %#v, %v", got, err)
	}
	for i, sections := range [][]configuration.TemplateSection{
		{{Name: ""}}, {{Name: "\xff"}}, {{Name: "rules"}, {Name: "rules"}},
		{{Name: "rules", Elements: []configuration.TemplateElement{{Element: configuration.Element{ID: element.Element.ID, Content: "{"}}}}},
	} {
		if err := s.Create(t.Context(), configuration.TemplateDraft{ID: "invalid", Sections: sections}); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid case %d: %v", i, err)
		}
	}
	for _, id := range []string{"template-A", "template-B"} {
		input := configuration.TemplateDraft{ID: id, Sections: []configuration.TemplateSection{first}}
		if err := s.Create(t.Context(), input); err != nil {
			t.Fatalf("create editable draft: %v", err)
		}
		got, err := s.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		decoded := got.Sections[0].Elements[0].Element
		if decoded.Content != element.Element.Content || len(decoded.Variables) != 1 ||
			decoded.Variables[0].Name != "port" || decoded.Variables[0].Type != "string" ||
			string(decoded.Variables[0].Default) != "\"\"" {
			t.Errorf("draft was not prepared with original authored content: %#v", decoded)
		}
		if len(input.Sections[0].Elements[0].Element.Variables) != 0 {
			t.Fatal("creation mutated input")
		}
	}
}
