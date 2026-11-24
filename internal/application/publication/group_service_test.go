package publication

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type groupStoreStub struct {
	inputs             *TemplateGroupInputs
	readErr, commitErr error
	reads              []string
	commits            []TemplateGroupPlan
}

func (s *groupStoreStub) ReadTemplateGroup(_ context.Context, id string) (*TemplateGroupInputs, error) {
	s.reads = append(s.reads, id)
	return s.inputs, s.readErr
}

func (s *groupStoreStub) CommitTemplateGroup(_ context.Context, plan TemplateGroupPlan) (*TemplateGroupResult, error) {
	s.commits = append(s.commits, plan)
	if s.commitErr != nil {
		return nil, s.commitErr
	}
	result := &TemplateGroupResult{TemplateID: plan.TemplateID, Version: plan.TemplateDraftVersion, Routers: make([]GroupRouterResult, 0, len(plan.Members))}
	if plan.Edit != nil {
		result.Version++
	}
	for _, member := range plan.Members {
		outcome := GroupRouterResult{RouterID: member.Expected.RouterID, Revision: member.Expected.DesiredNumber, Changed: member.NewRevision != nil}
		if outcome.Changed {
			outcome.Revision++
		}
		result.Routers = append(result.Routers, outcome)
	}
	return result, nil
}

const groupOutboundID = "550e8400-e29b-41d4-a716-446655440010"
const groupRuleID = "550e8400-e29b-41d4-a716-446655440011"
const groupNextRuleID = "550e8400-e29b-41d4-a716-446655440012"

func sharedFixture(t *testing.T, personal bool) (*TemplateGroupInputs, SharedPublishRequest) {
	t.Helper()
	draft := configuration.TemplateDraft{ID: "shared", Sections: []configuration.TemplateSection{
		{Name: "outbounds", Elements: []configuration.TemplateElement{{Element: configuration.Element{
			ID: groupOutboundID, Content: `{"protocol":"freedom","tag":"direct"}`,
		}}}},
	}}
	if personal {
		draft.Sections = append(draft.Sections, configuration.TemplateSection{Name: "rules", Elements: []configuration.TemplateElement{{Element: configuration.Element{
			ID: groupRuleID, Content: `{"type":"field","network":"tcp","outboundTag":"direct"}`,
		}}}})
	}
	accepted := &templates.Snapshot{TemplateDraft: draft, Version: 1}
	in := &TemplateGroupInputs{TemplateDraft: accepted, Accepted: accepted}
	for _, id := range []string{"inheriting.example", "replacing.example"} {
		personalDraft := routersettings.Draft{ID: id, TemplateID: draft.ID}
		var basis *OrderBasis
		if personal {
			personalDraft.PersonalOrder = []string{groupRuleID}
			basis = &OrderBasis{TemplateID: draft.ID, TemplateVersion: 1, SharedRuleIDs: []string{groupRuleID}}
		}
		if id == "replacing.example" {
			personalDraft.Groups = []routersettings.Group{{Name: "outbounds", Overrides: []configuration.ContentOverride{{
				Source: configuration.ElementSource{TemplateID: draft.ID, ElementID: groupOutboundID}, Mode: configuration.ContentModeReplacement,
				Content: json.RawMessage(`{"protocol":"freedom","tag":"direct"}`),
			}}}}
		}
		candidate, issues := PrepareRouterCandidate(accepted, &personalDraft, basis)
		if len(issues) != 0 {
			t.Fatal(issues)
		}
		in.Members = append(in.Members, TemplateGroupMember{
			Active:  ActiveSettings{Draft: personalDraft, Generation: 1, OrderBasis: basis},
			Desired: Revision{Number: 1, Catalog: VerifiedFiles{Profile: configuration.XrayIdentityVersion, Files: candidate.Catalog.Files}},
		})
	}
	return in, SharedPublishRequest{TemplateID: draft.ID, TemplateDraftVersion: 1, AcceptedVersion: 1, Source: SourceSharedDraft}
}

func changedSharedFixture(in *TemplateGroupInputs) configuration.TemplateDraft {
	draft := in.TemplateDraft.TemplateDraft
	draft.Sections = append([]configuration.TemplateSection(nil), draft.Sections...)
	draft.Sections[0].Elements = append([]configuration.TemplateElement(nil), draft.Sections[0].Elements...)
	draft.Sections[0].Elements[0].Element.Content = `{"protocol":"blackhole","tag":"direct"}`
	return draft
}

func TestSharedPublishSelectsSourcesAndKeepsUnchangedRouter(t *testing.T) {
	for _, source := range []SharedSource{SourceSharedCandidate, SourceSharedDraft} {
		t.Run(string(source), func(t *testing.T) {
			input, request := sharedFixture(t, false)
			update := changedSharedFixture(input)
			request.Source = source
			if source == SourceSharedCandidate {
				request.Candidate = &update
			} else {
				request.TemplateDraftVersion = 2
				input.TemplateDraft = &templates.Snapshot{TemplateDraft: update, Version: 2}
			}
			store := &groupStoreStub{inputs: input}
			calls := 0
			validator := catalogValidatorFunc(func(_ context.Context, profile string, files []configuration.CatalogFile) error {
				calls++
				if profile != configuration.XrayIdentityVersion || len(files) != 5 {
					t.Fatal("wrong validator request")
				}
				return nil
			})
			got, err := NewTemplateGroupService(store, validator).Publish(t.Context(), request)
			want := &TemplateGroupResult{TemplateID: "shared", Version: 2, Routers: []GroupRouterResult{
				{RouterID: "inheriting.example", Revision: 2, Changed: true}, {RouterID: "replacing.example", Revision: 1},
			}}
			if err != nil || !reflect.DeepEqual(got, want) || calls != 2 || len(store.commits) != 1 {
				t.Fatalf("publish=%#v %v, checks=%d commits=%#v", got, err, calls, store.commits)
			}
			commit := store.commits[0]
			if (commit.Edit != nil) != (source == SourceSharedCandidate) ||
				commit.Members[0].NewRevision == nil || commit.Members[1].NewRevision != nil {
				t.Fatalf("wrong plan: %#v", commit)
			}
		})
	}
}

func TestSharedPublishRejectsStalePreviewBeforeValidation(t *testing.T) {
	for _, members := range [][]GroupRouterExpectation{
		{},
		{{RouterID: "inheriting.example", ActiveGeneration: 1, DesiredNumber: 1}, {RouterID: "replacing.example", ActiveGeneration: 2, DesiredNumber: 1}},
	} {
		in, req := sharedFixture(t, false)
		req.PreviewMembers = members
		store := &groupStoreStub{inputs: in}
		validator := catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error {
			t.Fatal("stale preview reached Xray")
			return nil
		})
		got, err := NewTemplateGroupService(store, validator).Publish(t.Context(), req)
		if got != nil || !errors.Is(err, ErrConflict) || len(store.commits) != 0 {
			t.Fatalf("publication of stale preview = %#v %v", got, err)
		}
	}
}

func TestSharedPublishReconcilesPersonalOrders(t *testing.T) {
	in, req := sharedFixture(t, true)
	update := changedSharedFixture(in)
	rules := append([]configuration.TemplateElement(nil), update.Sections[1].Elements...)
	update.Sections[1].Elements = append([]configuration.TemplateElement{{Element: configuration.Element{
		ID: groupNextRuleID, Content: `{"type":"field","network":"udp","outboundTag":"direct"}`,
	}}}, rules...)
	req.Source, req.Candidate = SourceSharedCandidate, &update
	store := &groupStoreStub{inputs: in}
	ok := catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error { return nil })
	got, err := NewTemplateGroupService(store, ok).Publish(t.Context(), req)
	if err != nil || got == nil || len(store.commits) != 1 {
		t.Fatalf("publish = %#v, %v", got, err)
	}
	for _, member := range store.commits[0].Members {
		if !reflect.DeepEqual(member.PersonalOrder, []string{groupNextRuleID, groupRuleID}) ||
			member.OrderBasis == nil || member.OrderBasis.TemplateVersion != 2 || member.NewRevision == nil {
			t.Fatalf("incorrect member plan: %#v", member)
		}
	}
}

func TestSharedPublishDoesNotCommitWhenLaterValidationFails(t *testing.T) {
	for _, test := range []struct {
		name  string
		later func([]configuration.CatalogFile) error
		want  error
	}{
		{"rejected", func(_ []configuration.CatalogFile) error {
			return errors.Join(ErrCatalogRejected, errors.New("demo-private-value"))
		}, ErrCatalogRejected},
		{"unavailable", func(_ []configuration.CatalogFile) error { return errors.New("demo-private-value") }, ErrValidatorUnavailable},
		{"changed verification", func(files []configuration.CatalogFile) error { files[0].Content[0] = 'x'; return ErrCatalogRejected }, ErrValidatorUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			in, req := sharedFixture(t, false)
			store := &groupStoreStub{inputs: in}
			calls := 0
			v := catalogValidatorFunc(func(_ context.Context, _ string, files []configuration.CatalogFile) error {
				calls++
				if calls == 2 {
					return test.later(files)
				}
				return nil
			})
			got, err := NewTemplateGroupService(store, v).Publish(t.Context(), req)
			var member *GroupMemberError
			if got != nil || !errors.Is(err, test.want) || !errors.As(err, &member) || member.RouterID != "replacing.example" ||
				strings.Contains(err.Error(), "demo-private-value") || calls != 2 || len(store.commits) != 0 {
				t.Fatalf("later failure = %#v %v, checks=%d commits=%#v", got, err, calls, store.commits)
			}
		})
	}
}

func TestSharedPublishEmptyGroupDoesNotRequireValidator(t *testing.T) {
	in, req := sharedFixture(t, false)
	in.Members = nil
	in.Accepted = nil
	req.AcceptedVersion = 0
	store := &groupStoreStub{inputs: in}
	got, err := NewTemplateGroupService(store, nil).Publish(t.Context(), req)
	if err != nil || got == nil || got.Version != 1 || got.Routers == nil || len(got.Routers) != 0 || len(store.commits) != 1 {
		t.Fatalf("empty publish = %#v %v commits=%#v", got, err, store.commits)
	}
}

func TestSharedPublishReturnsNoPartialResultOnCommitFailure(t *testing.T) {
	in, req := sharedFixture(t, false)
	store := &groupStoreStub{inputs: in, commitErr: ErrConflict}
	calls := 0
	v := catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error { calls++; return nil })
	got, err := NewTemplateGroupService(store, v).Publish(t.Context(), req)
	if got != nil || !errors.Is(err, ErrConflict) || calls != 2 || len(store.commits) != 1 {
		t.Fatalf("failed commit = %#v, %v; checks=%d commits=%#v", got, err, calls, store.commits)
	}
}

func TestSharedPublishOwnsAnInlineDraftBeforeValidation(t *testing.T) {
	in, req := sharedFixture(t, false)
	update := changedSharedFixture(in)
	req.Source, req.Candidate = SourceSharedCandidate, &update
	store := &groupStoreStub{inputs: in}
	v := catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error {
		update.Sections[0].Elements[0].Element.Content = "null"
		return nil
	})
	got, err := NewTemplateGroupService(store, v).Publish(t.Context(), req)
	if err != nil || got == nil || len(store.commits) != 1 ||
		store.commits[0].Edit.Sections[0].Elements[0].Element.Content != "{\"protocol\":\"blackhole\",\"tag\":\"direct\"}" {
		t.Fatalf("unchecked draft accepted: %#v %v %#v", got, err, store.commits)
	}
}
