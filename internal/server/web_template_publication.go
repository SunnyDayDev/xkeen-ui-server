package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

type webTemplatePublicationService interface {
	Preview(context.Context, string, string, int64) (*webadmin.TemplatePublicationPreview, error)
	Confirm(context.Context, string, string, string, string) (*publication.TemplateGroupResult, error)
	Current(context.Context, string, string) (*webadmin.TemplatePublicationCurrent, error)
}

type webPublicationMember struct {
	RouterID        string                          `json:"routerId"`
	Name            string                          `json:"name,omitempty"`
	DesiredRevision string                          `json:"desiredRevision"`
	Contact         reporting.Presence              `json:"contact,omitempty"`
	Estimate        string                          `json:"estimate,omitempty"`
	Changes         []publication.CatalogFileChange `json:"changes,omitempty"`
	Changed         *bool                           `json:"changed,omitempty"`
}

type webPublicationResponse struct {
	State           string                 `json:"state"`
	TemplateID      string                 `json:"templateId"`
	Version         string                 `json:"version,omitempty"`
	AcceptedVersion string                 `json:"acceptedVersion"`
	Members         []webPublicationMember `json:"members"`
	Receipt         string                 `json:"receipt,omitempty"`
}

func NewWebTemplatePublicationHandler(lifetime context.Context, origin WebOrigin, service webTemplatePublicationService, admission webPreparationAdmission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/templates/publication" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebEndpoint(w, r, origin, true, http.MethodGet, http.MethodPost) {
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		ids := query["templateId"]
		if err != nil || len(query) != 1 || len(ids) != 1 || ids[0] == "" || !utf8.ValidString(ids[0]) {
			webResult(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		token, id := webSessionToken(r), ids[0]
		var reply webPublicationResponse
		if r.Method == http.MethodGet {
			result, err := service.Current(r.Context(), token, id)
			if err != nil {
				webPublicationFailure(w, err)
				return
			}
			reply = webPublicationResponse{State: "current", TemplateID: result.TemplateID, Version: strconv.FormatInt(result.Version, 10),
				AcceptedVersion: strconv.FormatInt(result.AcceptedVersion, 10), Members: make([]webPublicationMember, 0, len(result.Routers))}
			for _, item := range result.Routers {
				reply.Members = append(reply.Members, webPublicationMember{RouterID: item.RouterID, Name: item.Name, DesiredRevision: strconv.FormatInt(item.Desired, 10)})
			}
		} else {
			ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
			defer cancel()
			stop := context.AfterFunc(lifetime, cancel)
			defer stop()
			if lifetime.Err() != nil {
				cancel()
			}
			csrf := webCSRFProof(r)
			if err := admission.CheckMutation(ctx, token, csrf); err != nil {
				webFailure(w, err)
				return
			}
			var ok bool
			reply, ok = prepareWebPublication(w, r.WithContext(ctx), service, admission, token, csrf, id)
			if !ok {
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	})
}

func prepareWebPublication(w http.ResponseWriter, r *http.Request, service webTemplatePublicationService, admission webPreparationAdmission, token, csrf, id string) (webPublicationResponse, bool) {
	var input struct {
		Action  string          `json:"action"`
		Version json.RawMessage `json:"version"`
		Receipt json.RawMessage `json:"receipt"`
	}
	if !readWebJSON(w, r, 16<<10, &input) {
		return webPublicationResponse{}, false
	}
	failure := func(err error) (webPublicationResponse, bool) {
		// Invalidation during validation must also suppress failure diagnostics.
		if check := admission.CheckMutation(r.Context(), token, csrf); check != nil {
			err = check
		}
		webPublicationFailure(w, err)
		return webPublicationResponse{}, false
	}
	switch input.Action {
	case "preview":
		var raw string
		if input.Receipt != nil || json.Unmarshal(input.Version, &raw) != nil {
			break
		}
		version, ok := variableGeneration(raw, 1)
		if !ok {
			break
		}
		result, err := service.Preview(r.Context(), token, id, version)
		if err != nil {
			return failure(err)
		}
		if result.Preview == nil || len(result.Preview.Members) != len(result.Contact) {
			return failure(publication.ErrStoredState)
		}
		reply := webPublicationResponse{State: "preview", TemplateID: result.Preview.TemplateID, Version: strconv.FormatInt(result.Preview.Version, 10),
			AcceptedVersion: strconv.FormatInt(result.Preview.AcceptedVersion, 10), Receipt: result.Receipt,
			Members: make([]webPublicationMember, 0, len(result.Preview.Members))}
		for i, item := range result.Preview.Members {
			reply.Members = append(reply.Members, webPublicationMember{RouterID: item.Expected.RouterID, Name: item.Name,
				DesiredRevision: strconv.FormatInt(item.Expected.DesiredNumber, 10), Contact: result.Contact[i], Estimate: item.Estimate, Changes: item.Changes})
		}
		return reply, true
	case "confirm":
		var receipt string
		if input.Version != nil || json.Unmarshal(input.Receipt, &receipt) != nil || receipt == "" {
			break
		}
		result, err := service.Confirm(r.Context(), token, csrf, id, receipt)
		if err != nil {
			return failure(err)
		}
		reply := webPublicationResponse{State: "published", TemplateID: result.TemplateID, AcceptedVersion: strconv.FormatInt(result.Version, 10),
			Members: make([]webPublicationMember, 0, len(result.Routers))}
		for _, item := range result.Routers {
			reply.Members = append(reply.Members, webPublicationMember{RouterID: item.RouterID,
				DesiredRevision: strconv.FormatInt(item.Revision, 10), Changed: &item.Changed})
		}
		return reply, true
	}
	webResult(w, http.StatusBadRequest, "invalid_request", "")
	return webPublicationResponse{}, false
}

func webPublicationFailure(w http.ResponseWriter, err error) {
	var invalid *publication.PreparationError
	switch {
	case errors.Is(err, webadmin.ErrTemplatePreviewChanged):
		webResult(w, http.StatusConflict, "preview_changed", "")
	case errors.Is(err, publication.ErrSourceMissing):
		webResult(w, http.StatusNotFound, "template_unavailable", "")
	case errors.Is(err, publication.ErrCatalogRejected):
		webResult(w, http.StatusUnprocessableEntity, "catalog_rejected", "")
	case errors.Is(err, publication.ErrValidatorUnavailable):
		webResult(w, http.StatusServiceUnavailable, "validator_unavailable", "")
	case errors.Is(err, publication.ErrBypassUnavailable):
		webResult(w, http.StatusUnprocessableEntity, "bypass_unavailable", "")
	case errors.Is(err, publication.ErrInvalidCandidate), errors.As(err, &invalid):
		webResult(w, http.StatusUnprocessableEntity, "catalog_unready", "")
	default:
		webFailure(w, err)
	}
}
