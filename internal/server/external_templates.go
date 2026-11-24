package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/templates"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type ExternalTemplateStore interface {
	CreateExternalTemplate(context.Context, externalaccess.Permit, string) (templates.Summary, error)
	ListExternalTemplates(context.Context, externalaccess.Permit) ([]templates.Summary, error)
	ReplaceExternalTemplate(context.Context, externalaccess.Permit, configuration.TemplateDraft, int64) (int64, error)
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

func NewExternalTemplates(store ExternalTemplateStore) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		w.Header().Set("Cache-Control", "no-store, no-transform")
		if r.URL.Path == "/api/v1/templates" {
			if r.Method != http.MethodGet && r.Method != http.MethodPost {
				w.Header().Set("Allow", "GET, POST")
				externalError(w, 405, "method_not_allowed")
				return
			}
			if r.URL.RawQuery != "" || r.URL.ForceQuery {
				externalError(w, 400, "invalid_request")
				return
			}
			if r.Method == http.MethodPost {
				createExternalTemplate(w, r, store, permit)
				return
			}
			summaries, err := store.ListExternalTemplates(r.Context(), permit)
			if err != nil {
				externalFailure(w, err)
				return
			}
			writeTemplateSummaries(w, summaries)
			return
		}
		if r.URL.Path != "/api/v1/templates/item" {
			externalError(w, 404, "not_found")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPatch {
			w.Header().Set("Allow", "GET, PATCH")
			externalError(w, 405, "method_not_allowed")
			return
		}
		params, err := url.ParseQuery(r.URL.RawQuery)
		ids := params["templateId"]
		if err != nil || len(params) != 1 || len(ids) != 1 || ids[0] == "" || !utf8.ValidString(ids[0]) {
			externalError(w, 400, "invalid_request")
			return
		}
		var edit externalEdit
		var condition string
		if r.Method == http.MethodPatch {
			var ok bool
			edit, condition, ok = readExternalEdit(w, r)
			if !ok {
				return
			}
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
		if r.Method == http.MethodPatch {
			if condition != tag {
				externalError(w, 412, "precondition_failed")
				return
			}
			candidate, err := templates.EditElementContent(s, edit.ElementID, *edit.Source)
			if err != nil {
				var invalid *templates.InvalidDraftError
				switch {
				case errors.Is(err, templates.ErrElementNotFound):
					externalError(w, 404, "not_found")
				case errors.As(err, &invalid):
					externalError(w, 422, "invalid_content")
				default:
					externalFailure(w, err)
				}
				return
			}
			version, err := store.ReplaceExternalTemplate(r.Context(), permit, candidate, s.Version)
			if err != nil {
				if errors.Is(err, templates.ErrVersionConflict) {
					externalError(w, 412, "precondition_failed")
				} else if errors.Is(err, templates.ErrNotFound) {
					externalError(w, 404, "not_found")
				} else {
					externalFailure(w, err)
				}
				return
			}
			data, tag, err = draftRepresentation(&templates.Snapshot{TemplateDraft: candidate, Version: version})
			if err != nil {
				externalFailure(w, err)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", tag)
		_, _ = w.Write(data)
	}
}

func createExternalTemplate(w http.ResponseWriter, r *http.Request, store ExternalTemplateStore, permit externalaccess.Permit) {
	var request struct {
		ID string `json:"id"`
	}
	if !readExternalTemplateJSON(w, r, 8<<10, &request) {
		return
	}
	if request.ID == "" {
		externalError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	created, err := store.CreateExternalTemplate(r.Context(), permit, request.ID)
	switch {
	case errors.Is(err, templates.ErrConflict):
		externalError(w, http.StatusConflict, "template_exists")
	case errors.Is(err, templates.ErrInvalid):
		externalError(w, http.StatusBadRequest, "invalid_request")
	case err != nil:
		externalFailure(w, err)
	default:
		query := url.Values{"templateId": {created.ID}}
		w.Header().Set("Location", "/api/v1/templates/item?"+query.Encode())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(templateSummaryResponse(created))
	}
}

type externalEdit struct {
	ElementID string  `json:"elementId"`
	Source    *string `json:"source"`
}

var externalDraftTag = regexp.MustCompile(`^"[0-9a-f]{64}"$`)

func readExternalEdit(w http.ResponseWriter, r *http.Request) (externalEdit, string, bool) {
	var edit externalEdit
	tags := r.Header.Values("If-Match")
	if len(tags) == 0 {
		externalError(w, 428, "precondition_required")
		return edit, "", false
	}
	if len(tags) != 1 || !externalDraftTag.MatchString(tags[0]) {
		externalError(w, 400, "invalid_request")
		return edit, "", false
	}
	if !readExternalTemplateJSON(w, r, maxWebEditorBody, &edit) {
		return edit, "", false
	}
	if !configuration.ValidElementID(edit.ElementID) || edit.Source == nil {
		externalError(w, http.StatusBadRequest, "invalid_request")
		return edit, "", false
	}
	return edit, tags[0], true
}

func readExternalTemplateJSON(w http.ResponseWriter, r *http.Request, limit int64, out any) bool {
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if len(r.Header.Values("Content-Type")) != 1 || err != nil || mt != "application/json" || len(params) != 0 {
		externalError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return false
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	var size *http.MaxBytesError
	if errors.As(err, &size) {
		externalError(w, http.StatusRequestEntityTooLarge, "too_large")
		return false
	}
	if err != nil || jsonv2.Unmarshal(raw, out, jsonv2.RejectUnknownMembers(true)) != nil {
		externalError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}
