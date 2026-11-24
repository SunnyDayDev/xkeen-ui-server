package publication

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type publicationStoreStub struct {
	inputs             *Inputs
	readErr, commitErr error
	reads              []ReadRequest
	commits            []CommitPlan
}

func (s *publicationStoreStub) Read(_ context.Context, req ReadRequest) (*Inputs, error) {
	s.reads = append(s.reads, req)
	return s.inputs, s.readErr
}

func (s *publicationStoreStub) Commit(_ context.Context, plan CommitPlan) (int64, error) {
	s.commits = append(s.commits, plan)
	number := plan.Expected.DesiredNumber
	if plan.NewRevision != nil {
		number++
	}
	return number, s.commitErr
}

type catalogValidatorFunc func(context.Context, string, []configuration.CatalogFile) error

func (f catalogValidatorFunc) Validate(ctx context.Context, profile string, files []configuration.CatalogFile) error {
	return f(ctx, profile, files)
}

func activePublicationFixture() (PublishRequest, *Inputs) {
	template, router := snapshots()
	router.Name = "active"
	inputs := &Inputs{
		RouterID: router.ID, TemplateID: template.ID, Accepted: template,
		Active:  &ActiveSettings{Draft: *router, Generation: 3},
		Desired: &Revision{Number: 7, Catalog: VerifiedFiles{Profile: configuration.XrayIdentityVersion}},
	}
	request := PublishRequest{Mode: PublishOrdinary, Source: SourceActive, Expected: Expectations{
		RouterID: router.ID, TemplateID: template.ID, ActiveGeneration: 3, AcceptedVersion: template.Version, DesiredNumber: 7,
	}}
	return request, inputs
}

func TestPublishActiveUsesOnlyAcceptedState(t *testing.T) {
	request, inputs := activePublicationFixture()
	store := &publicationStoreStub{inputs: inputs}
	templateDraft := *inputs.Accepted
	templateDraft.Version++
	templateDraft.Sections = []configuration.TemplateSection{{Name: "outbounds", Elements: []configuration.TemplateElement{
		{Element: configuration.Element{ID: firstID, Content: `"${neverResolved}"`}},
	}}}
	inputs.TemplateDraft = &templateDraft
	routerDraft := inputs.Active.Draft
	routerDraft.TemplateID = "pending-template"
	inputs.RouterDraft = &VersionedRouterDraft{Draft: routerDraft, Generation: 12}
	calls := 0
	service := NewService(store, catalogValidatorFunc(func(_ context.Context, profile string, files []configuration.CatalogFile) error {
		calls++
		if profile != configuration.XrayIdentityVersion || len(files) != 5 {
			t.Fatalf("validation profile=%q files=%#v", profile, files)
		}
		return nil
	}))
	result, err := service.Publish(t.Context(), request)
	if err != nil || result == nil || !result.Changed || result.Revision != 8 || calls != 1 {
		t.Fatalf("result = %#v, %v; validator calls = %d", result, err, calls)
	}
	if len(store.reads) != 1 || store.reads[0] != (ReadRequest{RouterID: request.Expected.RouterID}) {
		t.Fatalf("read approaches unused drafts: %#v", store.reads)
	}
	plan := store.commits[0]
	if plan.Settings.TemplateID != request.Expected.TemplateID || plan.Settings.Name != "active" ||
		plan.Expected.TemplateDraftVersion != nil || plan.Expected.RouterDraftGeneration != nil ||
		plan.NewRevision == nil || len(plan.NewRevision.Files) != 5 {
		t.Fatalf("commit uses wrong scope: %#v", plan)
	}
}

func TestPublishSavedDraftRequiresMatchingScope(t *testing.T) {
	for _, test := range []struct {
		name, template string
		generation     int64
		want           error
	}{
		{"same template", "template-demo", 4, nil},
		{"pending switch", "pending", 4, ErrSourceMismatch},
		{"changed draft", "template-demo", 5, ErrConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, inputs := activePublicationFixture()
			request.Source = SourceSavedDraft
			seenGeneration := int64(4)
			request.Expected.RouterDraftGeneration = &seenGeneration
			draft := inputs.Active.Draft
			draft.Name, draft.TemplateID = "saved", test.template
			inputs.RouterDraft = &VersionedRouterDraft{Draft: draft, Generation: test.generation}
			inputs.RouterDraftRequested = true
			store := &publicationStoreStub{inputs: inputs}
			calls := 0
			service := NewService(store, catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error {
				calls++
				return nil
			}))
			got, err := service.Publish(t.Context(), request)
			if !errors.Is(err, test.want) || (got != nil) != (test.want == nil) {
				t.Fatalf("publish = %#v, %v; want %v", got, err, test.want)
			}
			if len(store.reads) != 1 || !store.reads[0].IncludeRouterDraft || store.reads[0].IncludeTemplateDraft {
				t.Fatalf("wrong read request: %#v", store.reads)
			}
			if test.want != nil {
				if calls != 0 || len(store.commits) != 0 {
					t.Fatalf("unready draft reached validation or commit")
				}
				return
			}
			if calls != 1 || len(store.commits) != 1 || store.commits[0].Settings.Name != "saved" ||
				*store.commits[0].Expected.RouterDraftGeneration != seenGeneration {
				t.Fatalf("saved draft not selected: %#v", store.commits)
			}
		})
	}
}

func TestPublishRejectsWrongExpectedStateBeforeValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*PublishRequest, *Inputs)
		want   error
		reads  int
	}{
		{"missing mode", func(req *PublishRequest, _ *Inputs) { req.Mode = "" }, ErrInvalidCommand, 0},
		{"missing scope", func(req *PublishRequest, _ *Inputs) { req.Expected.RouterID = "" }, ErrInvalidCommand, 0},
		{"editable token on active", func(req *PublishRequest, _ *Inputs) { req.Expected.RouterDraftGeneration = new(int64(2)) }, ErrInvalidCommand, 0},
		{"stale accepted", func(req *PublishRequest, _ *Inputs) { req.Expected.AcceptedVersion++ }, ErrConflict, 1},
		{"stale active", func(req *PublishRequest, _ *Inputs) { req.Expected.ActiveGeneration++ }, ErrConflict, 1},
		{"stale desired", func(req *PublishRequest, _ *Inputs) { req.Expected.DesiredNumber++ }, ErrConflict, 1},
		{"wrong scope", func(_ *PublishRequest, in *Inputs) { in.RouterID = "different" }, ErrConflict, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, inputs := activePublicationFixture()
			test.mutate(&request, inputs)
			store := &publicationStoreStub{inputs: inputs}
			service := NewService(store, catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error {
				t.Fatal("invalid input reached validation")
				return nil
			}))
			got, err := service.Publish(t.Context(), request)
			if got != nil || !errors.Is(err, test.want) || len(store.reads) != test.reads || len(store.commits) != 0 {
				t.Fatalf("publish = %#v, %v; reads=%#v commits=%#v", got, err, store.reads, store.commits)
			}
		})
	}
}

func TestPublishCandidateCommitsTheCheckedSettings(t *testing.T) {
	request, inputs := activePublicationFixture()
	local := json.RawMessage(`{"protocol":"freedom","tag":"submitted"}`)
	candidate := inputs.Active.Draft
	candidate.Name = "submitted"
	candidate.Groups = []routersettings.Group{{Name: "outbounds", Local: []routersettings.LocalElement{
		{ID: firstID, Content: local},
	}}}
	request.Source, request.Candidate = SourceCandidate, &candidate
	store := &publicationStoreStub{inputs: inputs}
	var validatorCopy []configuration.CatalogFile
	service := NewService(store, catalogValidatorFunc(func(_ context.Context, _ string, files []configuration.CatalogFile) error {
		candidate.Name = "caller mutates after preparation"
		local[0] = 'x'
		validatorCopy = files
		return nil
	}))
	got, err := service.Publish(t.Context(), request)
	if err != nil || got == nil || len(store.commits) != 1 {
		t.Fatalf("publish = %#v, %v", got, err)
	}
	plan := store.commits[0]
	validatorCopy[3].Content[0] = 'x'
	if plan.Settings.Name != "submitted" ||
		string(plan.Settings.Groups[0].Local[0].Content) != `{"protocol":"freedom","tag":"submitted"}` ||
		plan.NewRevision == nil || !strings.Contains(string(plan.NewRevision.Files[3].Content), `"tag": "submitted"`) ||
		plan.NewRevision.Files[3].Content[0] != '{' {
		t.Fatalf("commit payload changed after validation: %#v", plan)
	}
}

func TestPublishRejectsInvalidCandidateAndUnknownBasis(t *testing.T) {
	for _, test := range []struct {
		name  string
		draft func(*routersettings.Draft)
		want  error
		code  string
	}{
		{"invalid draft", func(d *routersettings.Draft) {
			d.Groups = []routersettings.Group{{Name: "outbounds", Local: []routersettings.LocalElement{
				{ID: firstID, Content: json.RawMessage(`{"private":`)},
			}}}
		}, ErrInvalidCandidate, ""},
		{"missing basis", func(d *routersettings.Draft) { d.PersonalOrder = []string{} }, nil, "order_basis_required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, inputs := activePublicationFixture()
			draft := inputs.Active.Draft
			test.draft(&draft)
			request.Source, request.Candidate = SourceCandidate, &draft
			store := &publicationStoreStub{inputs: inputs}
			service := NewService(store, catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error {
				t.Fatal("invalid candidate reached validation")
				return nil
			}))
			got, err := service.Publish(t.Context(), request)
			if got != nil || err == nil || len(store.commits) != 0 {
				t.Fatalf("publish = %#v, %v", got, err)
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error = %v; want %v", err, test.want)
			}
			if test.code != "" {
				var failure *PreparationError
				if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != test.code {
					t.Fatalf("preparation error = %#v", err)
				}
			}
		})
	}
}

func TestPublishValidatesEqualCatalogWithoutANewRevision(t *testing.T) {
	request, inputs := activePublicationFixture()
	prepared, issues := PrepareRouterCandidate(inputs.Accepted, &inputs.Active.Draft, nil)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	inputs.Desired.Catalog.Files = slices.Clone(prepared.Catalog.Files)
	slices.Reverse(inputs.Desired.Catalog.Files)
	store := &publicationStoreStub{inputs: inputs}
	calls := 0
	service := NewService(store, catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error {
		calls++
		return nil
	}))
	got, err := service.Publish(t.Context(), request)
	if err != nil || got == nil || got.Revision != 7 || got.Changed || calls != 1 ||
		len(store.commits) != 1 || store.commits[0].NewRevision != nil {
		t.Fatalf("equal publication = %#v, %v; validation=%d commits=%#v", got, err, calls, store.commits)
	}
	inputs.Desired.Catalog.Files = inputs.Desired.Catalog.Files[1:]
	got, err = service.Publish(t.Context(), request)
	if err != nil || got == nil || got.Revision != 8 || !got.Changed || calls != 2 ||
		len(store.commits) != 2 || store.commits[1].NewRevision == nil {
		t.Fatalf("changed file set = %#v, %v; commits=%#v", got, err, store.commits)
	}
}

func TestPublishRefusesUnverifiedCatalogs(t *testing.T) {
	for _, test := range []struct {
		name      string
		validator CatalogValidator
		profile   string
		want      error
	}{
		{"missing validator", nil, configuration.XrayIdentityVersion, ErrValidatorUnavailable},
		{"rejected", catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error {
			return errors.Join(ErrCatalogRejected, errors.New("demo-private-value"))
		}), configuration.XrayIdentityVersion, ErrCatalogRejected},
		{"unavailable", catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error {
			return errors.Join(ErrValidatorUnavailable, errors.New("demo-private-value"))
		}), configuration.XrayIdentityVersion, ErrValidatorUnavailable},
		{"unknown failure", catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error {
			return errors.New("demo-private-value")
		}), configuration.XrayIdentityVersion, ErrValidatorUnavailable},
		{"modified payload", catalogValidatorFunc(func(_ context.Context, _ string, files []configuration.CatalogFile) error {
			files[0].Content[0] = 'x'
			return nil
		}), configuration.XrayIdentityVersion, ErrValidatorUnavailable},
		{"modified and rejected payload", catalogValidatorFunc(func(_ context.Context, _ string, files []configuration.CatalogFile) error {
			files[0].Content[0] = 'x'
			return ErrCatalogRejected
		}), configuration.XrayIdentityVersion, ErrValidatorUnavailable},
		{"incompatible saved profile", nil, "unsupported-profile", ErrUnsupportedProfile},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, inputs := activePublicationFixture()
			inputs.Desired.Catalog.Profile = test.profile
			store := &publicationStoreStub{inputs: inputs}
			got, err := NewService(store, test.validator).Publish(t.Context(), request)
			if got != nil || !errors.Is(err, test.want) || strings.Contains(err.Error(), "demo-private-value") || len(store.commits) != 0 {
				t.Fatalf("publish = %#v, %v; want %v", got, err, test.want)
			}
		})
	}
}

func TestPublishFirstConfirmationUsesOnlyExplicitSharedSource(t *testing.T) {
	for _, mode := range []PublishMode{ConfirmAccepted, ConfirmInitial} {
		t.Run(string(mode), func(t *testing.T) {
			request, inputs := activePublicationFixture()
			draft := inputs.Active.Draft
			request.Mode, request.Source, request.Candidate = mode, SourceCandidate, &draft
			request.Expected.ActiveGeneration, request.Expected.DesiredNumber = 0, 0
			inputs.Active, inputs.Desired = nil, nil
			initial := mode == ConfirmInitial
			if initial {
				inputs.TemplateDraft, inputs.Accepted = inputs.Accepted, nil
				inputs.TemplateDraftRequested = true
				request.Expected.TemplateDraftVersion = new(inputs.TemplateDraft.Version)
				request.Expected.AcceptedVersion = 0
			}
			store := &publicationStoreStub{inputs: inputs}
			validator := catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error { return nil })
			got, err := NewService(store, validator).Publish(t.Context(), request)
			if err != nil || got == nil || got.Revision != 1 || !got.Changed || len(store.commits) != 1 {
				t.Fatalf("first confirmation = %#v, %v; commits=%#v", got, err, store.commits)
			}
			if len(store.reads) != 1 || store.reads[0] != (ReadRequest{
				RouterID: request.Expected.RouterID, InitialTemplateID: request.Expected.TemplateID, IncludeTemplateDraft: initial,
			}) {
				t.Fatalf("first confirmation read = %#v", store.reads)
			}
			plan := store.commits[0]
			if !plan.FirstConfirmation || plan.AcceptInitialTemplate != initial || plan.NewRevision == nil ||
				(plan.Expected.TemplateDraftVersion != nil) != initial {
				t.Fatalf("first confirmation commit = %#v", plan)
			}
		})
	}
}

func TestPublishFirstConfirmationDoesNotFallbackOrChangeAnAssignment(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*PublishRequest, *Inputs)
	}{
		{"keeps current assignment", func(_ *PublishRequest, _ *Inputs) {}},
		{"accepted source became absent", func(_ *PublishRequest, in *Inputs) { in.Active, in.Desired, in.Accepted = nil, nil, nil }},
		{"new accepted snapshot blocks bootstrap", func(req *PublishRequest, in *Inputs) {
			in.Active, in.Desired = nil, nil
			req.Mode = ConfirmInitial
			req.Expected.AcceptedVersion = 0
			req.Expected.TemplateDraftVersion = new(in.Accepted.Version)
			in.TemplateDraft, in.TemplateDraftRequested = in.Accepted, true
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, inputs := activePublicationFixture()
			draft := inputs.Active.Draft
			request.Mode, request.Source, request.Candidate = ConfirmAccepted, SourceCandidate, &draft
			request.Expected.ActiveGeneration, request.Expected.DesiredNumber = 0, 0
			test.mutate(&request, inputs)
			store := &publicationStoreStub{inputs: inputs}
			validator := catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error {
				t.Fatal("incompatible confirmation reached validation")
				return nil
			})
			got, err := NewService(store, validator).Publish(t.Context(), request)
			if got != nil || !errors.Is(err, ErrConflict) || len(store.commits) != 0 {
				t.Fatalf("refused confirmation = %#v, %v", got, err)
			}
		})
	}
}

func TestPublishFinalizationReturnsSafeConflictsAndFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{"concurrent publication", ErrConflict, ErrConflict},
		{"failed commit", errors.New("demo-private-value"), ErrPublicationCommit},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, inputs := activePublicationFixture()
			store := &publicationStoreStub{inputs: inputs, commitErr: test.err}
			calls := 0
			validator := catalogValidatorFunc(func(context.Context, string, []configuration.CatalogFile) error {
				calls++
				return nil
			})
			got, err := NewService(store, validator).Publish(t.Context(), request)
			if got != nil || !errors.Is(err, test.want) || strings.Contains(err.Error(), "demo-private-value") ||
				calls != 1 || len(store.commits) != 1 || store.commits[0].Expected != request.Expected {
				t.Fatalf("finalization = %#v, %v, validation=%d commits=%#v", got, err, calls, store.commits)
			}
		})
	}
}

func TestPublishCarriesAnExplicitEmptyOrderAndItsBasis(t *testing.T) {
	request, inputs := activePublicationFixture()
	draft := inputs.Active.Draft
	draft.PersonalOrder = []string{}
	basis := &OrderBasis{
		TemplateID: inputs.TemplateID, TemplateVersion: inputs.Accepted.Version, SharedRuleIDs: []string{},
	}
	request.Source, request.Candidate, request.OrderBasis = SourceCandidate, &draft, basis
	store := &publicationStoreStub{inputs: inputs}
	validator := catalogValidatorFunc(func(_ context.Context, _ string, _ []configuration.CatalogFile) error {
		basis.TemplateVersion = 99
		draft.PersonalOrder = nil
		return nil
	})
	got, err := NewService(store, validator).Publish(t.Context(), request)
	if err != nil || got == nil || len(store.commits) != 1 {
		t.Fatalf("publish = %#v, %v", got, err)
	}
	plan := store.commits[0]
	if plan.OrderBasis == nil || plan.OrderBasis.TemplateVersion != request.Expected.AcceptedVersion ||
		plan.OrderBasis.SharedRuleIDs == nil || plan.Settings.PersonalOrder == nil {
		t.Fatalf("plan lost explicit empty order or basis: %#v", plan)
	}
}
