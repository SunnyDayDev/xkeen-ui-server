package routersettings

// VisibilitySnapshot binds a list observation to the exact active router and
// the last successful hide, including after the next accepted contact.
type VisibilitySnapshot struct {
	RouterID         string
	ActiveGeneration int64
	Epoch            int64
	Visible          bool
}
