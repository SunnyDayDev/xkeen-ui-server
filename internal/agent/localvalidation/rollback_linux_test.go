//go:build linux

package localvalidation

import (
	"slices"
	"testing"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
)

func TestRollbackRecognizesOnlyReachableExactCatalogs(t *testing.T) {
	file := func(name, value string, mode uint32) preparation.BaselineFile {
		return preparation.BaselineFile{Name: name, Content: []byte(value), UID: 0, GID: 8, Mode: mode}
	}
	oldLog := file("01_log.json", "old", 0640)
	newLog := file("01_log.json", "new", 0640)
	oldExtra := file("06_extra.json", "old extra", 0640)
	newExtra := file("06_extra.json", "new extra", 0640)
	for name, tc := range map[string]struct {
		actual, old, next []preparation.BaselineFile
		want              bool
	}{
		"optional removed":            {[]preparation.BaselineFile{newLog}, []preparation.BaselineFile{oldLog, oldExtra}, []preparation.BaselineFile{newLog}, true},
		"new optional":                {[]preparation.BaselineFile{oldLog, newExtra}, []preparation.BaselineFile{oldLog}, []preparation.BaselineFile{newLog, newExtra}, true},
		"optional changed but absent": {[]preparation.BaselineFile{newLog}, []preparation.BaselineFile{oldLog, oldExtra}, []preparation.BaselineFile{newLog, newExtra}, false},
		"mandatory absent":            {[]preparation.BaselineFile{oldExtra}, []preparation.BaselineFile{oldLog, oldExtra}, []preparation.BaselineFile{newLog}, false},
		"foreign metadata":            {[]preparation.BaselineFile{file("01_log.json", "new", 0600)}, []preparation.BaselineFile{oldLog}, []preparation.BaselineFile{newLog}, false},
		"external bytes":              {[]preparation.BaselineFile{file("01_log.json", "unexpected", 0640)}, []preparation.BaselineFile{oldLog}, []preparation.BaselineFile{newLog}, false},
		"duplicate":                   {[]preparation.BaselineFile{newLog, oldLog}, []preparation.BaselineFile{oldLog}, []preparation.BaselineFile{newLog}, false},
	} {
		t.Run(name, func(t *testing.T) {
			input := slices.Clone(tc.actual)
			if got := recoverableFiles(input, tc.old, tc.next); got != tc.want {
				t.Errorf("recoverable=%t, want %t", got, tc.want)
			}
		})
	}
}
