package restart

import "testing"

func TestHotJournalPhases(t *testing.T) {
	for _, phase := range []Phase{HotDispatch, HotVerified, HotFiles} {
		if !validOriginalPhase(phase) {
			t.Fatal(phase)
		}
	}
	if validOriginalPhase("hot_unknown") {
		t.Fatal("accepted unknown phase")
	}
}
