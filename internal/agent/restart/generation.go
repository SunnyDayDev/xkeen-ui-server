package restart

import (
	"bytes"
	"slices"
	"strings"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

// PlanWorkingGeneration preserves adopted metadata and assigns a fixed policy
// to the only extra managed filename that may appear after initial adoption.
func PlanWorkingGeneration(scope preparation.Scope, previous preparation.Baseline, target agentrevision.Snapshot, group uint32) ([]preparation.BaselineFile, error) {
	if group == 0 || !validBaseline(scope, previous) || target.Profile != scope.Profile {
		return nil, ErrBaseline
	}
	if _, err := agentrevision.Tag(scope.RouterID, target); err != nil {
		return nil, ErrDesired
	}
	adopted := make(map[string]preparation.BaselineFile, len(previous.Files))
	for _, file := range previous.Files {
		adopted[file.Name] = file
	}
	next := make([]preparation.BaselineFile, 0, len(target.Files))
	for _, file := range target.Files {
		expected, ok := adopted[file.Name]
		if !ok {
			if file.Name != "06_extra.json" {
				return nil, ErrBaseline
			}
			expected = preparation.BaselineFile{Name: file.Name, GID: group, Mode: 0640}
		}
		if expected.UID != 0 || expected.Mode&^0777 != 0 || expected.Mode&0022 != 0 ||
			(expected.Mode&0004 == 0 && (expected.GID != group || expected.Mode&0040 == 0)) {
			return nil, ErrBaseline
		}
		expected.Content = bytes.Clone(file.Content)
		next = append(next, expected)
	}
	slices.SortFunc(next, func(a, b preparation.BaselineFile) int { return strings.Compare(a.Name, b.Name) })
	return next, nil
}
