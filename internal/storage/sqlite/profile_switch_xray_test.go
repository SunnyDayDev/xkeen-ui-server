package sqlite

import (
	"errors"
	"os"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/validation"
)

func TestProfileSwitchWithIsolatedXray(t *testing.T) {
	socket := os.Getenv("XKEEN_TEST_VALIDATOR_SOCKET")
	if socket == "" {
		t.Skip("set XKEEN_TEST_VALIDATOR_SOCKET to the isolated Xray worker socket")
	}
	validator, err := validation.Local(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("restore", func(t *testing.T) { checkProfileSwitchSQLiteRoundTrip(t, validator) })
	t.Run("refused target", func(t *testing.T) {
		ctx := t.Context()
		db, _, _, _ := switchSQLiteFixture(t, validator)
		const invalid = "invalid-template"
		target := switchTemplate(invalid, "target")
		target.Sections[0].Elements[0].Element.Content = `{"protocol":"invalid-demo-protocol","tag":"target"}`
		snapshot, err := templates.New(NewTemplateRepository(db)).Create(ctx, target)
		if err != nil {
			t.Fatal(err)
		}
		storeRawAccepted(t, db, snapshot)
		service := publication.NewProfileSwitchService(NewPublications(db), validator)
		request := publication.ProfileSwitchRequest{RouterID: switchRouterID, TargetTemplateID: invalid}
		if result, err := service.Preview(ctx, request); result != nil || !errors.Is(err, publication.ErrCatalogRejected) {
			t.Fatalf("real rejected catalog: %#v %v", result, err)
		}
		current, err := service.Read(ctx, switchRouterID, switchHome)
		if err != nil || !current.AlreadyActive || current.Basis.DesiredNumber != 1 {
			t.Fatalf("invalid target modified current profile: %#v %v", current, err)
		}
	})
}
