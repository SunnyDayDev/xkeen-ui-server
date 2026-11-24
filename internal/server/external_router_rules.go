package server

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
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

type externalRuleMutation func(*publication.DraftRules) (routersettings.Draft, *publication.OrderBasis, error)

func NewExternalRouterRules(store externalRouterRulesStore, next ExternalOperation) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		if r.URL.Path != externalRulesPath {
			next(w, r, permit)
			return
		}
		w.Header().Set("Cache-Control", "no-store, no-transform")
		if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodPatch {
			w.Header().Set("Allow", "GET, POST, PATCH")
			externalError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		routerID, elementID, ok := routerElementQuery(r.URL.RawQuery)
		if !ok || elementID != "" {
			externalError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var expected string
		var mutation externalRuleMutation
		if r.Method != http.MethodGet {
			if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
				externalFailure(w, err)
				return
			}
			expected, ok = externalCondition(w, r)
			if !ok {
				return
			}
			if r.Method == http.MethodPost {
				mutation, ok = readExternalRuleInsert(w, r)
			} else {
				mutation, ok = readExternalRuleMove(w, r)
			}
			if !ok {
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
		if mutation != nil {
			if expected != tag {
				externalError(w, http.StatusPreconditionFailed, "precondition_failed")
				return
			}
			draft, orderBasis, err := mutation(rules)
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

func readExternalRuleInsert(w http.ResponseWriter, r *http.Request) (externalRuleMutation, bool) {
	var input struct {
		After  *string `json:"afterId"`
		Source *string `json:"source"`
	}
	if !readExternalTemplateJSON(w, r, maxWebEditorBody, &input) {
		return nil, false
	}
	if input.After == nil || input.Source == nil || (*input.After != "" && !configuration.ValidElementID(*input.After)) {
		externalError(w, http.StatusBadRequest, "invalid_request")
		return nil, false
	}
	return func(rules *publication.DraftRules) (routersettings.Draft, *publication.OrderBasis, error) {
		id, err := uuid.NewRandom()
		if err != nil {
			return routersettings.Draft{}, nil, err
		}
		return rules.Insert(id.String(), *input.After, json.RawMessage(*input.Source))
	}, true
}

func readExternalRuleMove(w http.ResponseWriter, r *http.Request) (externalRuleMutation, bool) {
	var input struct {
		Action   string          `json:"action"`
		RuleID   json.RawMessage `json:"ruleId"`
		Position json.RawMessage `json:"position"`
		Confirm  json.RawMessage `json:"confirm"`
	}
	if !readExternalTemplateJSON(w, r, 16<<10, &input) {
		return nil, false
	}
	var id string
	var position *int
	var valid bool
	switch input.Action {
	case "move":
		valid = input.Confirm == nil && jsonv2.Unmarshal(input.RuleID, &id) == nil && configuration.ValidElementID(id) &&
			jsonv2.Unmarshal(input.Position, &position) == nil && position != nil && *position >= 0
	case "reset":
		var confirmed bool
		valid = input.RuleID == nil && input.Position == nil && jsonv2.Unmarshal(input.Confirm, &confirmed) == nil && confirmed
	}
	if !valid {
		externalError(w, http.StatusBadRequest, "invalid_request")
		return nil, false
	}
	return func(rules *publication.DraftRules) (routersettings.Draft, *publication.OrderBasis, error) {
		if position != nil && *position >= len(rules.Entries) {
			return routersettings.Draft{}, nil, routersettings.ErrInvalidDraft
		}
		return rules.Reorder(id, position)
	}, true
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
