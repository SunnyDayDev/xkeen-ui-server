package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

type ExternalRouterDraftStore interface {
	ReadExternalRouterDraft(context.Context, externalaccess.Permit, string) (routerdraft.Snapshot, error)
	WriteExternalRouterDraft(context.Context, externalaccess.Permit, routerdraft.Basis, routersettings.Draft) (int64, error)
}

type externalRouterMutation func(routerdraft.Snapshot, string) (routersettings.Draft, error)
type externalRouterPatchReader func(http.ResponseWriter, *http.Request) (externalRouterMutation, bool)
type externalRouterRepresentation func(routerdraft.Snapshot, string) ([]byte, string, error)

func externalRouterElementOperation(path string, store ExternalRouterDraftStore, representation externalRouterRepresentation,
	readPatch externalRouterPatchReader, next ExternalOperation) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		if r.URL.Path != path {
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
		var mutation externalRouterMutation
		if r.Method == http.MethodPatch {
			if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
				externalFailure(w, err)
				return
			}
			expected, ok = externalCondition(w, r)
			if !ok {
				return
			}
			mutation, ok = readPatch(w, r)
			if !ok {
				return
			}
		}
		snapshot, err := store.ReadExternalRouterDraft(r.Context(), permit, routerID)
		if err != nil {
			externalRouterFailure(w, err)
			return
		}
		body, tag, err := representation(snapshot, elementID)
		if err != nil {
			externalRouterFailure(w, err)
			return
		}
		if r.Method == http.MethodPatch {
			if expected != tag {
				externalError(w, http.StatusPreconditionFailed, "precondition_failed")
				return
			}
			snapshot.Editable, err = mutation(snapshot, elementID)
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
			body, tag, err = representation(snapshot, elementID)
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

type externalRouterBasis struct {
	RouterID         string `json:"routerId"`
	TemplateID       string `json:"templateId"`
	AcceptedVersion  string `json:"acceptedVersion"`
	ActiveGeneration string `json:"activeGeneration"`
	DraftGeneration  string `json:"draftGeneration"`
}

func externalRouterBasisFrom(basis routerdraft.Basis) externalRouterBasis {
	return externalRouterBasis{
		RouterID: basis.RouterID, TemplateID: basis.TemplateID,
		AcceptedVersion:  strconv.FormatInt(basis.AcceptedVersion, 10),
		ActiveGeneration: strconv.FormatInt(basis.ActiveGeneration, 10),
		DraftGeneration:  strconv.FormatInt(basis.DraftGeneration, 10),
	}
}

func externalRouterDocument(scope string, result any) ([]byte, string, error) {
	body, err := json.Marshal(result)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte(scope + "\x00"))
	_, _ = digest.Write(body)
	return body, fmt.Sprintf(`"%x"`, digest.Sum(nil)), nil
}

func externalRouterFailure(w http.ResponseWriter, err error) {
	var local *publication.LocalRuleSourceError
	var order *publication.PreparationError
	var invalid *routersettings.VariableSourceError
	var replacement *routersettings.ReplacementSourceError
	switch {
	case errors.As(err, &local):
		externalError(w, 422, "invalid_source")
	case errors.As(err, &order), errors.Is(err, publication.ErrConflict), errors.Is(err, publication.ErrInvalidCandidate):
		externalError(w, 409, "order_unavailable")
	case errors.Is(err, errRuleAnalysis):
		externalError(w, 409, "analysis_unavailable")
	case errors.Is(err, routerdraft.ErrMissing):
		externalError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, routerdraft.ErrConflict):
		externalError(w, http.StatusPreconditionFailed, "precondition_failed")
	case errors.Is(err, routerdraft.ErrForeign), errors.Is(err, routersettings.ErrSharedScope):
		externalError(w, http.StatusConflict, "different_template")
	case errors.Is(err, routersettings.ErrVariableMode):
		externalError(w, http.StatusConflict, "replacement_mode")
	case errors.Is(err, errExternalReplacementMode):
		externalError(w, http.StatusConflict, "incompatible_mode")
	case errors.Is(err, routersettings.ErrInvalidDraft):
		externalError(w, http.StatusBadRequest, "invalid_request")
	case errors.As(err, &replacement):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(struct {
			Code   string `json:"code"`
			Offset *int64 `json:"offset,omitempty"`
		}{"invalid_source", replacement.Issue.Offset})
	case errors.As(err, &invalid):
		externalError(w, http.StatusUnprocessableEntity, "invalid_source")
	default:
		externalFailure(w, err)
	}
}
