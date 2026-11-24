package webadmin

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type ExclusionVariable struct {
	Name    string  `json:"name"`
	Default *string `json:"default,omitempty"`
}

// An absent shared source has no ID and version zero.
type ExclusionDraft struct {
	ID        string
	Version   int64
	Text      string
	Variables []ExclusionVariable
}

func (s ExclusionDraft) Source() configuration.ExclusionSource {
	src := configuration.ExclusionSource{Text: s.Text, Variables: make([]configuration.VariableDefinition, len(s.Variables))}
	for i, v := range s.Variables {
		def := configuration.VariableDefinition{Name: v.Name, Type: "string"}
		if v.Default != nil {
			def.Default, _ = json.Marshal(*v.Default)
		}
		src.Variables[i] = def
	}
	return src
}

func (s ExclusionDraft) Valid() bool {
	if !utf8.ValidString(s.Text) {
		return false
	}
	for _, v := range s.Variables {
		if !utf8.ValidString(v.Name) || v.Default != nil && !utf8.ValidString(*v.Default) {
			return false
		}
	}
	src := s.Source()
	// Validate definition names/uniqueness without requiring addresses to be ready.
	src.Text = ""
	_, issue := configuration.AssembleXKeenExclusions(&src, nil, nil)
	return issue == nil
}
