package webadmin

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type stubCatalogPreview struct {
	readErr, checkErr error
	checked           bool
}

func (s *stubCatalogPreview) ReadCatalog(_ context.Context, _ [32]byte, _ func() time.Time, id string, mode publication.RouterPreviewMode) (*publication.Inputs, error) {
	return &publication.Inputs{
		RouterID: id, TemplateID: "template-a", Accepted: &templates.Snapshot{TemplateDraft: configuration.TemplateDraft{ID: "template-a"}, Version: 3},
		Active:               &publication.ActiveSettings{Draft: routersettings.Draft{ID: id, TemplateID: "template-a"}, Generation: 5},
		Desired:              &publication.Revision{Number: 9, Catalog: publication.VerifiedFiles{Profile: configuration.XrayIdentityVersion}},
		RouterDraftRequested: mode == publication.PreviewPersonal,
	}, s.readErr
}
func (s *stubCatalogPreview) CheckCatalog(_ context.Context, _ [32]byte, _ func() time.Time, mode publication.RouterPreviewMode, scope publication.Expectations) error {
	s.checked = mode == publication.PreviewPersonal && scope.RouterID == "router" && scope.DesiredNumber == 9 && scope.AcceptedVersion == 3 &&
		scope.RouterDraftGeneration != nil && *scope.RouterDraftGeneration == 0
	return s.checkErr
}

func TestWebRouterCatalogRechecksSessionAndScopeAfterAssembly(t *testing.T) {
	store := &stubCatalogPreview{}
	service := NewRouterCatalogPreview(store, time.Now)
	token := base64.RawURLEncoding.EncodeToString(make([]byte, TokenBytes))
	for _, err := range []error{ErrSignedOut, publication.ErrConflict, nil} {
		store.checkErr = err
		result, got := service.Read(t.Context(), token, "router", publication.PreviewPersonal)
		if !store.checked || !errors.Is(got, err) || (result == nil) != (err != nil) {
			t.Fatalf("protected preview %+v %v", result, got)
		}
	}
	if result, err := service.Read(t.Context(), "not valid", "router", publication.PreviewPersonal); result != nil || !errors.Is(err, ErrSignedOut) {
		t.Fatalf("unauthorized fields: %+v %v", result, err)
	}
}
