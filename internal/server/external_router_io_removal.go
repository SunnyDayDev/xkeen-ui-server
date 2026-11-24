package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type externalIORemovalView struct {
	externalRouterBasis
	ElementID string                       `json:"elementId"`
	Section   string                       `json:"section"`
	Origin    string                       `json:"origin"`
	Effect    string                       `json:"effect"`
	Blockers  []publication.DraftIOBlocker `json:"blockers"`
}

func NewExternalRouterIORemoval(store ExternalRouterDraftStore, next ExternalOperation) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		if r.URL.Path != "/api/v1/routers/io/item" {
			next(w, r, permit)
			return
		}
		w.Header().Set("Cache-Control", "no-store, no-transform")
		if r.Method != http.MethodGet && r.Method != http.MethodDelete {
			w.Header().Set("Allow", "GET, DELETE")
			externalError(w, 405, "method_not_allowed")
			return
		}
		deleting := r.Method == http.MethodDelete
		routerID, section, elementID, valid := externalIORemovalQuery(r.URL.RawQuery, deleting)
		if !valid || r.ContentLength != 0 {
			externalError(w, 400, "invalid_request")
			return
		}
		var expected string
		if deleting {
			expected, valid = externalCondition(w, r)
			if !valid {
				return
			}
		}
		snapshot, err := store.ReadExternalRouterDraft(r.Context(), permit, routerID)
		if err != nil {
			externalRouterFailure(w, err)
			return
		}
		plan, planErr := publication.PrepareDraftIORemoval(snapshot.Accepted, snapshot.Editable, snapshot.OrderBasis, section, elementID)
		if !recheckExternalRouterDraft(w, r, store, permit, snapshot.Basis) {
			return
		}
		if planErr != nil {
			externalIORemovalFailure(w, planErr)
			return
		}
		view := externalIORemovalView{externalRouterBasisFrom(snapshot.Basis), elementID, section, plan.Origin, plan.Effect, plan.Blockers}
		body, tag, err := externalRouterDocument("external-io-removal", view)
		if err != nil {
			externalFailure(w, err)
			return
		}
		if !deleting {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("ETag", tag)
			_, _ = w.Write(body)
			return
		}
		if expected != tag {
			externalError(w, 412, "precondition_failed")
			return
		}
		if len(plan.Blockers) != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(struct {
				Code     string                       `json:"code"`
				Blockers []publication.DraftIOBlocker `json:"blockers"`
			}{"element_in_use", plan.Blockers})
			return
		}
		generation, err := store.WriteExternalRouterDraft(r.Context(), permit, snapshot.Basis, plan.Draft)
		if err != nil {
			externalRouterFailure(w, err)
			return
		}
		snapshot.Basis.DraftGeneration = generation
		view.externalRouterBasis = externalRouterBasisFrom(snapshot.Basis)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(view)
	}
}

func externalIORemovalQuery(raw string, deleting bool) (routerID, section, elementID string, valid bool) {
	query, err := url.ParseQuery(raw)
	if err != nil || len(query["routerId"]) != 1 || len(query["section"]) != 1 || len(query["elementId"]) != 1 {
		return "", "", "", false
	}
	if deleting {
		if len(query) != 4 || len(query["confirm"]) != 1 || query["confirm"][0] != "remove" {
			return "", "", "", false
		}
	} else if len(query) != 3 {
		return "", "", "", false
	}
	routerID, section, elementID = query["routerId"][0], query["section"][0], query["elementId"][0]
	valid = routerID != "" && utf8.ValidString(routerID) &&
		(section == "inbounds" || section == "outbounds") && configuration.ValidElementID(elementID)
	return
}

func externalIORemovalFailure(w http.ResponseWriter, err error) {
	var invalid *publication.PreparationError
	switch {
	case errors.Is(err, publication.ErrSourceMissing):
		externalRouterFailure(w, routerdraft.ErrMissing)
	case errors.Is(err, publication.ErrSourceMismatch):
		externalRouterFailure(w, routerdraft.ErrForeign)
	case errors.As(err, &invalid):
		externalError(w, 409, "analysis_unavailable")
	default:
		externalRouterFailure(w, err)
	}
}
