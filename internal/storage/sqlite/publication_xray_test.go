package sqlite

import (
	"os"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/validation"
)

func TestSingleRouterPublicationWithIsolatedXray(t *testing.T) {
	socket := os.Getenv("XKEEN_TEST_VALIDATOR_SOCKET")
	if socket == "" {
		t.Skip("set XKEEN_TEST_VALIDATOR_SOCKET to a private socket for the isolated Xray worker")
	}
	validator, err := validation.Local(socket)
	if err != nil {
		t.Fatal(err)
	}
	checkSingleRouterPublication(t, validator)
}
