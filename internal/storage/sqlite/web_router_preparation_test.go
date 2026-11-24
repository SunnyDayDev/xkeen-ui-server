package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

func TestWebRouterPreparationSourceUsesAuthorizedAcceptedSnapshot(t *testing.T) {
	ctx := t.Context()
	db, _, digest, now := fixtureWebTemplateWriter(t)
	repository := NewWebRouterPreparation(NewPublications(db))
	service := templates.New(NewTemplateRepository(db))
	const id = "exact / 1"
	draft := configuration.TemplateDraft{ID: id}
	if _, err := service.Create(ctx, draft); err != nil {
		t.Fatal(err)
	}
	source, err := repository.ReadSource(ctx, digest, fixedWebTime(now), id)
	if err != nil || source.Kind != webadmin.PreparationDraft || source.Snapshot.ID != id || source.Snapshot.Version != 1 {
		t.Fatalf("first source: %#v %v", source, err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO accepted_templates(template_id,version,draft) SELECT id,version,draft FROM templates WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	draft.Sections = []configuration.TemplateSection{{Name: "outbounds"}}
	if _, err := service.Replace(ctx, draft, 1); err != nil {
		t.Fatal(err)
	}
	source, err = repository.ReadSource(ctx, digest, fixedWebTime(now), id)
	if err != nil || source.Kind != webadmin.PreparationAccepted || source.Snapshot.Version != 1 || len(source.Snapshot.Sections) != 0 {
		t.Fatalf("newer editing source shadowed accepted state: %#v %v", source, err)
	}
	if _, err := repository.ReadSource(ctx, digest, fixedWebTime(now), "other"); !errors.Is(err, webadmin.ErrPreparationSourceMissing) {
		t.Fatalf("missing source: %v", err)
	}
	if err := NewWebAdmin(db).SaveVerifier(ctx, webadmin.Verifier{Version: 1}, true); err != nil {
		t.Fatal(err)
	}
	if source, err := repository.ReadSource(ctx, digest, fixedWebTime(now), id); source.Snapshot != nil || !errors.Is(err, webadmin.ErrSignedOut) {
		t.Fatalf("read private source after reset: %#v %v", source, err)
	}
}

func TestWebRouterPreparationC06CommitAndRevocation(t *testing.T) {
	for _, row := range []struct {
		mode     string
		expected error
	}{
		{"success", nil},
		{"rejected", publication.ErrCatalogRejected},
		{"unconfigured", publication.ErrValidatorUnavailable},
		{"revoked", webadmin.ErrSignedOut},
		{"changed", publication.ErrConflict},
	} {
		t.Run(row.mode, func(t *testing.T) {
			ctx := t.Context()
			db, _, _, now := fixtureWebTemplateWriter(t)
			actions := NewWebAdmin(db)
			raw := [webadmin.TokenBytes]byte{17}
			token := base64.RawURLEncoding.EncodeToString(raw[:])
			if err := actions.Issue(ctx, 1, sha256.Sum256(raw[:]), [32]byte{}, now); err != nil {
				t.Fatal(err)
			}
			session := webadmin.New(actions, fixedWebTime(now))
			csrf, err := session.Session(ctx, token, "", webadmin.SessionRead)
			if err != nil {
				t.Fatal(err)
			}

			const templateID = "edge / demo"
			const outbound = "550e8400-e29b-41d4-a716-446655440000"
			templatesService := templates.New(NewTemplateRepository(db))
			source := configuration.TemplateDraft{ID: templateID, Sections: []configuration.TemplateSection{{
				Name: "outbounds", Elements: []configuration.TemplateElement{{Element: configuration.Element{
					ID: outbound, Content: `{"protocol":"freedom","tag":"${label}"}`,
				}}},
			}}}
			if _, err := templatesService.Create(ctx, source); err != nil {
				t.Fatal(err)
			}
			notices := 0
			store := NewPublications(db, func(routerID string) {
				published, err := NewPublications(db).ReadDesired(ctx, routerID)
				if err != nil || published == nil || published.Number != 1 {
					t.Errorf("signal before commit: %#v %v", published, err)
				}
				notices++
			})
			var validator publication.CatalogValidator
			if row.mode != "unconfigured" {
				validator = testPublicationValidator(func(ctx context.Context, profile string, files []configuration.CatalogFile) error {
					if profile != configuration.XrayIdentityVersion || len(files) != 5 {
						t.Fatalf("unverified catalog: %q, %d files", profile, len(files))
					}
					switch row.mode {
					case "rejected":
						return publication.ErrCatalogRejected
					case "revoked":
						return actions.SaveVerifier(ctx, webadmin.Verifier{Version: 1}, true)
					case "changed":
						_, err := templatesService.Replace(ctx, source, 1)
						return err
					}
					return nil
				})
			}
			service := webadmin.NewRouterPreparation(NewWebRouterPreparation(store), validator, fixedWebTime(now))
			form, err := service.Describe(ctx, token, templateID)
			if err != nil || form.Kind != webadmin.PreparationDraft || !configuration.ValidElementID(form.RouterID) || len(form.Elements) != 1 {
				t.Fatalf("authorized read: %#v %v", form, err)
			}
			input := webadmin.PreparationConfirmation{RouterID: form.RouterID, Name: "Филиал Восток", TemplateID: templateID,
				Kind: form.Kind, Version: form.Version, Values: []webadmin.PersonalInput{{ElementID: outbound, Name: "label", Source: `"direct"`}}}
			result, err := service.Confirm(ctx, token, csrf, input)
			if !errors.Is(err, row.expected) {
				t.Fatalf("confirmation %#v, %v, want %v", result, err, row.expected)
			}
			persisted, err := store.Read(ctx, publication.ReadRequest{RouterID: form.RouterID, InitialTemplateID: templateID})
			if err != nil {
				t.Fatal(err)
			}
			if row.expected != nil {
				if persisted.Active != nil || persisted.Desired != nil || persisted.Accepted != nil || notices != 0 {
					t.Fatalf("failed confirmation left an effect: %#v, %d notices", persisted, notices)
				}
				return
			}
			if persisted.Active == nil || persisted.Active.Draft.Name != input.Name || persisted.Desired == nil || persisted.Desired.Number != 1 ||
				persisted.Accepted == nil || persisted.Accepted.Version != 1 || result.Revision != 1 || notices != 1 {
				t.Fatalf("missing first publication: %#v, %#v, %d notices", result, persisted, notices)
			}
			repeated, err := service.Prepared(ctx, token, form.RouterID)
			if err != nil || repeated != result {
				t.Fatalf("lookup after response loss: %#v %v", repeated, err)
			}
			if _, err := service.Confirm(ctx, token, csrf, input); !errors.Is(err, publication.ErrConflict) {
				t.Fatalf("unexpected repeat: %v", err)
			}
		})
	}
}
