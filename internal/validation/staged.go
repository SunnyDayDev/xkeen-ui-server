package validation

import (
	"io"
	"os"
	"path/filepath"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

// VerifyStagedCatalog runs inside the read-only runtime mount before invoking Xray.
func VerifyStagedCatalog(dir, expected string) bool {
	root, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer root.Close()
	entries, err := root.ReadDir(len(names) + 1)
	if err != nil && err != io.EOF {
		return false
	}
	if len(entries) < 5 || len(entries) > len(names) {
		return false
	}
	r := request{binding: binding{Profile: configuration.XrayIdentityVersion}}
	remaining := int64(maxCatalog)
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return false
		}
		f, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			return false
		}
		b, readErr := io.ReadAll(io.LimitReader(f, remaining+1))
		closeErr := f.Close()
		if readErr != nil || closeErr != nil || int64(len(b)) > remaining {
			return false
		}
		remaining -= int64(len(b))
		r.Files = append(r.Files, file{Name: entry.Name(), Bytes: b})
	}
	actual, ok := r.manifest()
	return ok && actual == expected
}
