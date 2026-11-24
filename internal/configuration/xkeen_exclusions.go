package configuration

import (
	"encoding/json"
	"net/netip"
	"strings"
	"unicode/utf8"
)

// ExclusionSource keeps authored text separate from the JSON Xray catalog.
type ExclusionSource struct {
	Text      string
	Variables []VariableDefinition
}

type ExclusionFile struct {
	Name    string
	Content []byte
}

// Line is one-based for invalid text, zero for source-wide metadata errors.
type ExclusionIssue struct {
	Code string
	Line int
}

// AssembleXKeenExclusions builds a complete file for the selected source.
// A nil result without an issue denotes the absence of a managed file.
func AssembleXKeenExclusions(source *ExclusionSource, personal map[string]json.RawMessage, replacement *string) (*ExclusionFile, *ExclusionIssue) {
	if source == nil {
		if replacement != nil || len(personal) > 0 {
			return nil, &ExclusionIssue{Code: "source_not_found"}
		}
		return nil, nil
	}
	text := source.Text
	defs := make(map[string]VariableDefinition)
	if replacement != nil {
		text = *replacement
	} else {
		for _, def := range source.Variables {
			if _, duplicate := defs[def.Name]; duplicate || !validVariableName(def.Name) || !utf8.ValidString(def.Name) || def.Type != "string" {
				return nil, &ExclusionIssue{Code: "invalid_definition"}
			}
			defs[def.Name] = def
		}
		for name := range personal {
			if _, ok := defs[name]; !ok {
				return nil, &ExclusionIssue{Code: "unknown_variable"}
			}
		}
	}
	var output strings.Builder
	seen := make(map[netip.Prefix]bool)
	lineNumber := 0
	for line := range strings.SplitSeq(text, "\n") {
		lineNumber++
		if !utf8.ValidString(line) {
			return nil, &ExclusionIssue{Code: "invalid_encoding", Line: lineNumber}
		}
		line, _, _ = strings.Cut(line, "#")
		line = strings.Trim(strings.TrimSuffix(line, "\r"), " \t")
		if line == "" {
			continue
		}
		if replacement == nil && strings.HasPrefix(line, "${") && strings.HasSuffix(line, "}") {
			name := line[2 : len(line)-1]
			def, ok := defs[name]
			if !ok {
				return nil, &ExclusionIssue{Code: "unknown_variable", Line: lineNumber}
			}
			values, issues := PrepareValues("", []VariableDefinition{def}, selectedExclusionValue(personal, name))
			if len(issues) > 0 {
				return nil, &ExclusionIssue{Code: issues[0].Code, Line: lineNumber}
			}
			if err := json.Unmarshal(values[name], &line); err != nil {
				return nil, &ExclusionIssue{Code: "invalid_value", Line: lineNumber}
			}
		}
		prefix, ok := exclusionPrefix(line)
		if !ok {
			return nil, &ExclusionIssue{Code: "invalid_address", Line: lineNumber}
		}
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		if prefix.IsSingleIP() {
			output.WriteString(prefix.Addr().String())
		} else {
			output.WriteString(prefix.String())
		}
		output.WriteByte('\n')
	}
	return &ExclusionFile{Name: "ip_exclude.lst", Content: []byte(output.String())}, nil
}

func selectedExclusionValue(personal map[string]json.RawMessage, name string) map[string]json.RawMessage {
	if value, ok := personal[name]; ok {
		return map[string]json.RawMessage{name: value}
	}
	return nil
}

func exclusionPrefix(s string) (netip.Prefix, bool) {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		a, err := netip.ParseAddr(s)
		if err != nil || a.Zone() != "" {
			return netip.Prefix{}, false
		}
		p = netip.PrefixFrom(a, a.BitLen())
	}
	return p, p.Bits() > 0 && !p.Addr().Is4In6() && p == p.Masked()
}
