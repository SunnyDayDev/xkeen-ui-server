package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
	"github.com/google/uuid"
)

const externalRulesPath = "/api/v1/routers/rules"

type externalRouterRulesStore interface {
	ReadExternalRouterDraft(context.Context, externalaccess.Permit, string) (routerdraft.Snapshot, error)
	WriteExternalRouterOrder(context.Context, externalaccess.Permit, routerdraft.Basis, routersettings.Draft, *publication.OrderBasis) (int64, error)
}

type externalRuleInsert struct {
	After  *string `json:"afterId"`
	Source *string `json:"source"`
}

func NewExternalRouterRules(store externalRouterRulesStore, next ExternalOperation) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		if r.URL.Path != externalRulesPath {
			next(w, r, permit)
			return
		}
		w.Header().Set("Cache-Control", "no-store, no-transform")
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, POST")
			externalError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		routerID, elementID, ok := routerElementQuery(r.URL.RawQuery)
		if !ok || elementID != "" {
			externalError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var expected string
		var insert externalRuleInsert
		if r.Method == http.MethodPost {
			if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
				externalFailure(w, err)
				return
			}
			expected, ok = externalCondition(w, r)
			if !ok || !readExternalTemplateJSON(w, r, maxWebEditorBody, &insert) {
				return
			}
			if insert.After == nil || insert.Source == nil || (*insert.After != "" && !configuration.ValidElementID(*insert.After)) {
				externalError(w, http.StatusBadRequest, "invalid_request")
				return
			}
		}

		snapshot, err := store.ReadExternalRouterDraft(r.Context(), permit, routerID)
		if err != nil {
			externalRouterFailure(w, err)
			return
		}
		rules, err := routerdraft.PrepareRules(snapshot)
		if err != nil {
			externalRuleFailure(w, err)
			return
		}
		body, tag, err := externalRuleRepresentation(snapshot.Basis, snapshot.Editable.PersonalOrder != nil, rules)
		if err != nil {
			externalFailure(w, err)
			return
		}
		if r.Method == http.MethodPost {
			if expected != tag {
				externalError(w, http.StatusPreconditionFailed, "precondition_failed")
				return
			}
			id, err := uuid.NewRandom()
			if err != nil {
				externalFailure(w, err)
				return
			}
			draft, orderBasis, err := rules.Insert(id.String(), *insert.After, json.RawMessage(*insert.Source))
			if err != nil {
				externalRuleFailure(w, err)
				return
			}
			updated, err := publication.PrepareDraftRules(snapshot.Accepted, draft, orderBasis)
			if err != nil {
				externalRuleFailure(w, err)
				return
			}
			generation, err := store.WriteExternalRouterOrder(r.Context(), permit, snapshot.Basis, draft, orderBasis)
			if err != nil {
				externalRuleFailure(w, err)
				return
			}
			basis := snapshot.Basis
			basis.DraftGeneration = generation
			body, tag, err = externalRuleRepresentation(basis, draft.PersonalOrder != nil, updated)
			if err != nil {
				externalFailure(w, err)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", tag)
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
		}
		_, _ = w.Write(body)
	}
}

func externalRuleRepresentation(basis routerdraft.Basis, personalized bool, draft *publication.DraftRules) ([]byte, string, error) {
	return externalRouterDocument("external-router-rules", struct {
		externalRouterBasis
		PersonalOrder bool                         `json:"personalOrder"`
		Rules         []publication.DraftRuleEntry `json:"rules"`
	}{externalRouterBasisFrom(basis), personalized, draft.Entries})
}

func externalRuleFailure(w http.ResponseWriter, err error) {
	var invalid *publication.LocalRuleSourceError
	var preparation *publication.PreparationError
	switch {
	case errors.As(err, &invalid):
		externalError(w, http.StatusUnprocessableEntity, "invalid_source")
	case errors.Is(err, publication.ErrConflict), errors.Is(err, publication.ErrInvalidCandidate):
		externalError(w, http.StatusConflict, "position_unavailable")
	case errors.As(err, &preparation):
		externalError(w, http.StatusConflict, "order_unavailable")
	default:
		externalRouterFailure(w, err)
	}
}
