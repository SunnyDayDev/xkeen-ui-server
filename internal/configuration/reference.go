package configuration

import "strings"

// parseTemplateString scans only original decoded template text. Its literal
// output and any eventual replacement must never be parsed as templates again.
func parseTemplateString(s string) (literal string, name *string, code string) {
	var out strings.Builder
	references := 0
	whole := false
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "$$"):
			out.WriteByte('$')
			i += 2
		case strings.HasPrefix(s[i:], "${"):
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				return "", nil, "invalid_reference"
			}
			end += i + 2
			candidate := s[i+2 : end]
			if !validVariableName(candidate) {
				return "", nil, "invalid_reference"
			}
			references++
			name = &candidate
			whole = i == 0 && end == len(s)-1
			i = end + 1
		default:
			out.WriteByte(s[i])
			i++
		}
	}
	if references == 0 {
		return out.String(), nil, ""
	}
	if references == 1 && whole {
		return "", name, ""
	}
	if references > 1 {
		name = nil
	}
	return "", name, "unsupported_interpolation"
}
