package routersettings

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var ErrDisplayNameConflict = errors.New("router display name changed")

// DisplayNameSnapshot binds the name to the exact device and independent
// display metadata version without advancing its configuration revisions.
type DisplayNameSnapshot struct {
	RouterID, Name            string
	ActiveGeneration, Version int64
}

func ValidDisplayName(name string) bool {
	return utf8.ValidString(name) && strings.TrimSpace(name) != ""
}
