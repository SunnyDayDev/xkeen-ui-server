package server

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/jsonvalue"
)

const maxWebEditorBody = 8 << 20

type webTemplateEditor interface {
	Read(context.Context, string, string) (*templates.Snapshot, error)
	Save(context.Context, string, string, string, webadmin.TemplateElementEdit) (*templates.Snapshot, string, error)
}

func NewWebTemplateEditorHandler(origin WebOrigin, editor webTemplateEditor, sessions *webadmin.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/templates/item" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebEndpoint(w, r, origin, true, http.MethodGet, http.MethodPost) {
			return
		}
		id, selected, ok := templateEditorQuery(r.URL.RawQuery, r.Method == http.MethodGet)
		if !ok {
			webResult(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		var snapshot *templates.Snapshot
		var err error
		if r.Method == http.MethodGet {
			snapshot, err = editor.Read(r.Context(), webSessionToken(r), id)
		} else {
			token := webSessionToken(r)
			var proof string
			if values := r.Header.Values("X-CSRF-Token"); len(values) == 1 {
				proof = values[0]
			}
			if err := sessions.CheckMutation(r.Context(), token, proof); err != nil {
				webFailure(w, err)
				return
			}
			input, status := readWebTemplateEdit(w, r)
			if status != 0 {
				switch status {
				case http.StatusUnsupportedMediaType:
					webResult(w, status, "unsupported_media_type", "")
				case http.StatusRequestEntityTooLarge:
					webResult(w, status, "request_too_large", "")
				default:
					webResult(w, status, "invalid_request", "")
				}
				return
			}
			snapshot, selected, err = editor.Save(r.Context(), token, proof, id, input)
		}
		if err != nil {
			webTemplateEditorFailure(w, err)
			return
		}
		result, ok := templateEditorResponse(snapshot, selected)
		if !ok {
			webResult(w, http.StatusNotFound, "element_not_found", "")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}

func templateEditorQuery(raw string, read bool) (id, selected string, ok bool) {
	values, err := url.ParseQuery(raw)
	if err != nil || len(values["templateId"]) != 1 || values.Get("templateId") == "" {
		return "", "", false
	}
	switch len(values) {
	case 1:
		return values.Get("templateId"), "", true
	case 2:
		if read && len(values["elementId"]) == 1 && configuration.ValidElementID(values.Get("elementId")) {
			return values.Get("templateId"), values.Get("elementId"), true
		}
	}
	return "", "", false
}

func readWebTemplateEdit(w http.ResponseWriter, r *http.Request) (webadmin.TemplateElementEdit, int) {
	invalid := webadmin.TemplateElementEdit{}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if len(r.Header.Values("Content-Type")) != 1 || err != nil || media != "application/json" || len(params) != 0 {
		return invalid, http.StatusUnsupportedMediaType
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebEditorBody))
	var sizeErr *http.MaxBytesError
	if errors.As(err, &sizeErr) {
		return invalid, http.StatusRequestEntityTooLarge
	}
	var input struct {
		Version   string          `json:"version"`
		Section   json.RawMessage `json:"section"`
		ElementID json.RawMessage `json:"elementId"`
		Source    *string         `json:"source"`
	}
	if err != nil || jsonv2.Unmarshal(raw, &input, jsonv2.RejectUnknownMembers(true)) != nil || input.Source == nil {
		return invalid, http.StatusBadRequest
	}
	version, err := strconv.ParseInt(input.Version, 10, 64)
	if err != nil || version < 1 || strconv.FormatInt(version, 10) != input.Version || (input.Section == nil) == (input.ElementID == nil) {
		return invalid, http.StatusBadRequest
	}
	edit := webadmin.TemplateElementEdit{Version: version, Source: *input.Source}
	if input.Section != nil {
		if jsonv2.Unmarshal(input.Section, &edit.Section) != nil || edit.Section == "" {
			return invalid, http.StatusBadRequest
		}
	} else if jsonv2.Unmarshal(input.ElementID, &edit.ElementID) != nil || !configuration.ValidElementID(edit.ElementID) {
		return invalid, http.StatusBadRequest
	}
	return edit, 0
}

type webEditorMember struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type webEditorSection struct {
	Name    string            `json:"name"`
	Members []webEditorMember `json:"members"`
}

type webEditorSelection struct {
	ID      string `json:"id"`
	Section string `json:"section"`
	Source  string `json:"source"`
}

type webEditorSnapshot struct {
	ID            string              `json:"id"`
	Version       string              `json:"version"`
	Sections      []webEditorSection  `json:"sections"`
	FirstSections []string            `json:"firstSections,omitempty"`
	Selected      *webEditorSelection `json:"selected,omitempty"`
}

func templateEditorResponse(snapshot *templates.Snapshot, selected string) (webEditorSnapshot, bool) {
	result := webEditorSnapshot{ID: snapshot.ID, Version: strconv.FormatInt(snapshot.Version, 10), Sections: make([]webEditorSection, 0, len(snapshot.Sections))}
	for _, section := range snapshot.Sections {
		group := webEditorSection{Name: section.Name, Members: make([]webEditorMember, 0, len(section.Elements))}
		for _, member := range section.Elements {
			item := member.Element
			label := item.ID
			if tree, issue := jsonvalue.Parse([]byte(item.Content)); issue == nil {
				if object, ok := tree.(map[string]any); ok {
					if tag, ok := object["tag"].(string); ok && tag != "" {
						label = tag
					}
				}
			}
			group.Members = append(group.Members, webEditorMember{ID: item.ID, Label: label})
			if item.ID == selected || (selected == "" && result.Selected == nil) {
				result.Selected = &webEditorSelection{ID: item.ID, Section: section.Name, Source: item.Content}
			}
		}
		result.Sections = append(result.Sections, group)
	}
	if result.Selected == nil {
		if selected != "" {
			return webEditorSnapshot{}, false
		}
		result.FirstSections = templates.FirstElementSections()
	}
	return result, true
}

func webTemplateEditorFailure(w http.ResponseWriter, err error) {
	var invalid *templates.InvalidDraftError
	switch {
	case errors.Is(err, templates.ErrCorrupt):
		webFailure(w, err)
	case errors.Is(err, templates.ErrNotFound):
		webResult(w, http.StatusNotFound, "template_not_found", "")
	case errors.Is(err, templates.ErrElementNotFound):
		webResult(w, http.StatusNotFound, "element_not_found", "")
	case errors.Is(err, templates.ErrVersionConflict), errors.Is(err, templates.ErrTemplateNotEmpty), errors.Is(err, templates.ErrVersionExhausted):
		webResult(w, http.StatusConflict, "version_conflict", "")
	case errors.As(err, &invalid) && len(invalid.Issues) > 0:
		issue := invalid.Issues[0]
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(struct {
			State  string  `json:"state"`
			Code   string  `json:"code"`
			Offset *int64  `json:"offset,omitempty"`
			Path   *string `json:"path,omitempty"`
		}{State: "invalid_source", Code: issue.Code, Offset: issue.Offset, Path: issue.Path})
	case errors.Is(err, templates.ErrInvalid):
		webResult(w, http.StatusBadRequest, "invalid_request", "")
	default:
		webFailure(w, err)
	}
}
