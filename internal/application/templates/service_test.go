package templates

import (
	"context"
	"errors"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type memoryRepository map[string]Snapshot

func (m memoryRepository) Insert(_ context.Context, draft configuration.TemplateDraft) error {
	if _, exists := m[draft.ID]; exists {
		return ErrConflict
	}
	copied, err := copyDraft(draft, configuration.RestoreElement)
	if err != nil {
		return err
	}
	m[draft.ID] = Snapshot{TemplateDraft: copied, Version: 1}
	return nil
}

func (m memoryRepository) Find(_ context.Context, id string) (*Snapshot, error) {
	snapshot, exists := m[id]
	if !exists {
		return nil, ErrNotFound
	}
	return &snapshot, nil
}

func (m memoryRepository) Replace(_ context.Context, draft configuration.TemplateDraft, expectedVersion int64) (int64, error) {
	current, exists := m[draft.ID]
	if !exists {
		return 0, ErrNotFound
	}
	if current.Version != expectedVersion {
		return 0, ErrVersionConflict
	}
	copied, err := copyDraft(draft, configuration.RestoreElement)
	if err != nil {
		return 0, err
	}
	m[draft.ID] = Snapshot{TemplateDraft: copied, Version: expectedVersion + 1}
	return expectedVersion + 1, nil
}

func TestCreateAndGet(t *testing.T) {
	s := New(memoryRepository{})
	for _, id := range []string{"demo", "Demo", " demo ", "demo\x00"} {
		created, err := s.Create(t.Context(), configuration.TemplateDraft{ID: id})
		if err != nil || created == nil || created.ID != id || len(created.Sections) != 0 || created.Version != 1 {
			t.Errorf("create %q: %#v, %v", id, created, err)
			continue
		}
		got, err := s.Get(t.Context(), id)
		if err != nil || got == nil || got.ID != id || len(got.Sections) != 0 || got.Version != 1 {
			t.Errorf("get %q: %#v, %v", id, got, err)
		}
	}
	if _, err := s.Create(t.Context(), configuration.TemplateDraft{ID: "demo"}); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate create: %v", err)
	}
	if got, err := s.Get(t.Context(), "unknown"); got != nil || !errors.Is(err, ErrNotFound) {
		t.Errorf("get unknown: %#v, %v", got, err)
	}
	for _, invalid := range []string{"", "\xff"} {
		if _, err := s.Create(t.Context(), configuration.TemplateDraft{ID: invalid}); !errors.Is(err, ErrInvalid) {
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
	_, err := s.Create(t.Context(), invalid)
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
		if _, err := s.Create(t.Context(), configuration.TemplateDraft{ID: "invalid", Sections: sections}); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid case %d: %v", i, err)
		}
	}
	for _, id := range []string{"template-A", "template-B"} {
		input := configuration.TemplateDraft{ID: id, Sections: []configuration.TemplateSection{first}}
		if _, err := s.Create(t.Context(), input); err != nil {
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

func TestReplaceVersionedDraft(t *testing.T) {
	ctx := t.Context()
	s := New(memoryRepository{})
	current, err := s.Create(ctx, configuration.TemplateDraft{
		ID: "demo", Sections: []configuration.TemplateSection{{Name: "removed"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	content := " { \"port\": \"${port}\", \"optional\":null }\n"
	candidate := configuration.TemplateDraft{ID: current.ID, Sections: []configuration.TemplateSection{
		{Name: "stats"},
		{Name: "rules", Elements: []configuration.TemplateElement{
			{Disabled: true, Element: configuration.Element{
				ID: "550e8400-e29b-41d4-a716-446655440000", Content: content,
			}},
		}},
	}}
	saved, err := s.Replace(ctx, candidate, current.Version)
	if err != nil || saved == nil || saved.Version != 2 || saved.ID != current.ID || len(saved.Sections) != 2 {
		t.Fatalf("replace: %#v, %v", saved, err)
	}
	element := saved.Sections[1].Elements[0].Element
	if element.Content != content || len(element.Variables) != 1 || element.Variables[0].Name != "port" {
		t.Fatalf("replacement was not prepared: %#v", element)
	}
	saved.Sections[0].Name = "returned result mutation"
	saved.Sections[1].Elements[0].Element.Variables[0].Default[0] = 'x'
	candidate.Sections[0].Name = "input mutation"
	got, err := s.Get(ctx, current.ID)
	if err != nil || got == nil || got.Version != 2 || got.Sections[0].Name != "stats" ||
		string(got.Sections[1].Elements[0].Element.Variables[0].Default) != `""` {
		t.Fatalf("replacement shares mutable data: %#v, %v", got, err)
	}
}

func TestRejectedReplacementPreservesDraft(t *testing.T) {
	ctx := t.Context()
	s := New(memoryRepository{})
	current, err := s.Create(ctx, configuration.TemplateDraft{
		ID: "demo", Sections: []configuration.TemplateSection{{Name: "current"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		candidate configuration.TemplateDraft
		version   int64
		want      error
	}{
		{"omitted version", configuration.TemplateDraft{ID: current.ID}, 0, ErrInvalidVersion},
		{"negative version", configuration.TemplateDraft{ID: current.ID}, -1, ErrInvalidVersion},
		{"wrong version", configuration.TemplateDraft{ID: current.ID}, 2, ErrVersionConflict},
		{"unknown ID", configuration.TemplateDraft{ID: "unknown"}, 1, ErrNotFound},
		{"invalid ID", configuration.TemplateDraft{}, 1, ErrInvalid},
		{"invalid candidate precedes conflict", configuration.TemplateDraft{ID: current.ID, Sections: []configuration.TemplateSection{{Name: "rules"}, {Name: "rules"}}}, 2, ErrInvalid},
		{"duplicate UUID", configuration.TemplateDraft{ID: current.ID, Sections: []configuration.TemplateSection{
			{Name: "rules", Elements: []configuration.TemplateElement{
				{Element: configuration.Element{ID: "550e8400-e29b-41d4-a716-446655440000", Content: "{}"}},
				{Disabled: true, Element: configuration.Element{ID: "550e8400-e29b-41d4-a716-446655440000", Content: "{}"}},
			}},
		}}, 1, ErrInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := s.Replace(ctx, test.candidate, test.version)
			if got != nil || !errors.Is(err, test.want) {
				t.Fatalf("replace = %#v, %v; want %v", got, err, test.want)
			}
			saved, err := s.Get(ctx, current.ID)
			if err != nil || saved == nil || saved.Version != 1 || len(saved.Sections) != 1 || saved.Sections[0].Name != "current" {
				t.Fatalf("rejected replacement changed the draft: %#v, %v", saved, err)
			}
		})
	}
}
