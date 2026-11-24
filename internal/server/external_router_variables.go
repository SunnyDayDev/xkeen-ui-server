package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

const externalVariablesPath = "/api/v1/routers/variables"

type ExternalRouterDraftStore interface {
	ReadExternalRouterDraft(context.Context, externalaccess.Permit, string) (routerdraft.Snapshot, error)
	WriteExternalRouterDraft(context.Context, externalaccess.Permit, routerdraft.Basis, routersettings.Draft) (int64, error)
}

func NewExternalRouterVariables(store ExternalRouterDraftStore, next ExternalOperation) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		if r.URL.Path != externalVariablesPath {
			next(w, r, permit)
			return
		}
		w.Header().Set("Cache-Control", "no-store, no-transform")
		if r.Method != http.MethodGet && r.Method != http.MethodPatch {
			w.Header().Set("Allow", "GET, PATCH")
			externalError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		routerID, elementID, ok := routerElementQuery(r.URL.RawQuery)
		if !ok || elementID == "" {
			externalError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var expected string
		var changes []routersettings.VariableChange
		if r.Method == http.MethodPatch {
			if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
				externalFailure(w, err)
				return
			}
			expected, ok = externalCondition(w, r)
			if !ok {
				return
			}
			changes, ok = externalVariableChanges(w, r)
			if !ok {
				return
			}
		}
		snapshot, err := store.ReadExternalRouterDraft(r.Context(), permit, routerID)
		if err != nil {
			externalRouterFailure(w, err)
			return
		}
		body, tag, err := externalVariableRepresentation(snapshot, elementID)
		if err != nil {
			externalRouterFailure(w, err)
			return
		}
		if r.Method == http.MethodPatch {
			if expected != tag {
				externalError(w, http.StatusPreconditionFailed, "precondition_failed")
				return
			}
			snapshot.Editable, err = routerdraft.EditVariables(snapshot, elementID, changes)
			if err != nil {
				externalRouterFailure(w, err)
				return
			}
			generation, err := store.WriteExternalRouterDraft(r.Context(), permit, snapshot.Basis, snapshot.Editable)
			if err != nil {
				externalRouterFailure(w, err)
				return
			}
			snapshot.Basis.DraftGeneration = generation
			body, tag, err = externalVariableRepresentation(snapshot, elementID)
			if err != nil {
				externalRouterFailure(w, err)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", tag)
		_, _ = w.Write(body)
	}
}

func externalCondition(w http.ResponseWriter, r *http.Request) (string, bool) {
	tags := r.Header.Values("If-Match")
	if len(tags) == 0 {
		externalError(w, http.StatusPreconditionRequired, "precondition_required")
		return "", false
	}
	if len(tags) != 1 || !externalDraftTag.MatchString(tags[0]) {
		externalError(w, http.StatusBadRequest, "invalid_request")
		return "", false
	}
	return tags[0], true
}

func externalVariableChanges(w http.ResponseWriter, r *http.Request) ([]routersettings.VariableChange, bool) {
	var input struct {
		Values []struct {
			Name   string          `json:"name"`
			Source json.RawMessage `json:"source"`
		} `json:"values"`
	}
	if !readExternalTemplateJSON(w, r, maxWebEditorBody, &input) {
		return nil, false
	}
	if len(input.Values) == 0 {
		externalError(w, http.StatusBadRequest, "invalid_request")
		return nil, false
	}
	changes := make([]routersettings.VariableChange, len(input.Values))
	for i, value := range input.Values {
		changes[i].Name = value.Name
		if value.Source != nil {
			var source *string
			if err := jsonv2.Unmarshal(value.Source, &source); err != nil || source == nil {
				externalError(w, http.StatusBadRequest, "invalid_request")
				return nil, false
			}
			changes[i].Value = json.RawMessage(*source)
		}
	}
	return changes, true
}

func externalVariableRepresentation(snapshot routerdraft.Snapshot, id string) ([]byte, string, error) {
	selection, err := routerdraft.InspectVariables(snapshot, id)
	if err != nil {
		return nil, "", err
	}
	body, err := json.Marshal(struct {
		RouterID         string `json:"routerId"`
		TemplateID       string `json:"templateId"`
		AcceptedVersion  string `json:"acceptedVersion"`
		ActiveGeneration string `json:"activeGeneration"`
		DraftGeneration  string `json:"draftGeneration"`
		routerdraft.VariableSelection
	}{
		RouterID: snapshot.Basis.RouterID, TemplateID: snapshot.Basis.TemplateID,
		AcceptedVersion:   strconv.FormatInt(snapshot.Basis.AcceptedVersion, 10),
		ActiveGeneration:  strconv.FormatInt(snapshot.Basis.ActiveGeneration, 10),
		DraftGeneration:   strconv.FormatInt(snapshot.Basis.DraftGeneration, 10),
		VariableSelection: selection,
	})
	if err != nil {
		return nil, "", err
	}
	return body, fmt.Sprintf(`"%x"`, sha256.Sum256(append([]byte("external-router-variables\x00"), body...))), nil
}

func externalRouterFailure(w http.ResponseWriter, err error) {
	var invalid *routersettings.VariableSourceError
	switch {
	case errors.Is(err, routerdraft.ErrMissing):
		externalError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, routerdraft.ErrConflict):
		externalError(w, http.StatusPreconditionFailed, "precondition_failed")
	case errors.Is(err, routerdraft.ErrForeign), errors.Is(err, routersettings.ErrSharedScope):
		externalError(w, http.StatusConflict, "different_template")
	case errors.Is(err, routersettings.ErrVariableMode):
		externalError(w, http.StatusConflict, "replacement_mode")
	case errors.Is(err, routersettings.ErrInvalidDraft):
		externalError(w, http.StatusBadRequest, "invalid_request")
	case errors.As(err, &invalid):
		externalError(w, http.StatusUnprocessableEntity, "invalid_source")
	default:
		externalFailure(w, err)
	}
}
