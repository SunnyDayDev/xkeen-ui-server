package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
)

type ExternalTemplateReader interface {
	ReadExternalTemplate(context.Context, externalaccess.Permit, string) (*templates.Snapshot, error)
}

type externalVariable struct {
	Name    string  `json:"name"`
	Type    string  `json:"type"`
	Default *string `json:"defaultSource,omitempty"`
}
type externalElement struct {
	ID        string             `json:"id"`
	Source    string             `json:"source"`
	Disabled  bool               `json:"disabled"`
	Variables []externalVariable `json:"variables"`
}
type externalSection struct {
	Name     string            `json:"name"`
	Elements []externalElement `json:"elements"`
}
type externalDraft struct {
	ID       string            `json:"id"`
	Version  string            `json:"version"`
	Sections []externalSection `json:"sections"`
}

func draftRepresentation(s *templates.Snapshot) ([]byte, string, error) {
	draft := externalDraft{ID: s.ID, Version: strconv.FormatInt(s.Version, 10), Sections: make([]externalSection, len(s.Sections))}
	for i, section := range s.Sections {
		part := externalSection{Name: section.Name, Elements: make([]externalElement, len(section.Elements))}
		for j, item := range section.Elements {
			element := externalElement{ID: item.Element.ID, Source: item.Element.Content, Disabled: item.Disabled, Variables: make([]externalVariable, len(item.Element.Variables))}
			for k, v := range item.Element.Variables {
				def := externalVariable{Name: v.Name, Type: v.Type}
				if v.Default != nil {
					value := string(v.Default)
					def.Default = &value
				}
				element.Variables[k] = def
			}
			part.Elements[j] = element
		}
		draft.Sections[i] = part
	}
	data, err := json.Marshal(draft)
	if err != nil {
		return nil, "", err
	}
	return data, fmt.Sprintf("\"%x\"", sha256.Sum256(data)), nil
}

func NewExternalTemplates(store ExternalTemplateReader) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		w.Header().Set("Cache-Control", "no-store, no-transform")
		if r.URL.Path != "/api/v1/templates/item" {
			externalError(w, 404, "not_found")
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			externalError(w, 405, "method_not_allowed")
			return
		}
		params, err := url.ParseQuery(r.URL.RawQuery)
		ids := params["templateId"]
		if err != nil || len(params) != 1 || len(ids) != 1 || ids[0] == "" || !utf8.ValidString(ids[0]) {
			externalError(w, 400, "invalid_request")
			return
		}
		s, err := store.ReadExternalTemplate(r.Context(), permit, ids[0])
		if err != nil {
			if errors.Is(err, templates.ErrNotFound) {
				externalError(w, 404, "not_found")
			} else {
				externalFailure(w, err)
			}
			return
		}
		data, tag, err := draftRepresentation(s)
		if err != nil {
			externalFailure(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", tag)
		_, _ = w.Write(data)
	}
}
