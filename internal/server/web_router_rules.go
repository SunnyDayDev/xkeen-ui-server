package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type webRouterRulesService interface {
	Read(context.Context, string, string) (webadmin.RouterRuleView, error)
	Insert(context.Context, string, string, webadmin.RouterDraftBasis, string, json.RawMessage) (webadmin.RouterRuleView, error)
	Reorder(context.Context, string, string, webadmin.RouterDraftBasis, string, *int) (webadmin.RouterRuleView, error)
}

func NewWebRouterRulesHandler(origin WebOrigin, service webRouterRulesService, admission webPreparationAdmission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/routers/rules" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebEndpoint(w, r, origin, true, http.MethodGet, http.MethodPost, http.MethodPatch) {
			return
		}
		routerID, elementID, ok := routerElementQuery(r.URL.RawQuery)
		if !ok || elementID != "" {
			webResult(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		token := webSessionToken(r)
		var result webadmin.RouterRuleView
		var err error
		if r.Method == http.MethodGet {
			result, err = service.Read(r.Context(), token, routerID)
		} else {
			csrf := webCSRFProof(r)
			if err := admission.CheckMutation(r.Context(), token, csrf); err != nil {
				webFailure(w, err)
				return
			}
			if r.Method == http.MethodPatch {
				basis, ruleID, position, ok := readWebRuleOrder(w, r, routerID)
				if !ok {
					return
				}
				result, err = service.Reorder(r.Context(), token, csrf, basis, ruleID, position)
			} else {
				var input struct {
					webRouterDraftInput
					After  *string `json:"afterId"`
					Source *string `json:"source"`
				}
				if !readWebJSON(w, r, maxWebEditorBody, &input) {
					return
				}
				basis, ok := input.basis(routerID)
				if !ok || input.After == nil || input.Source == nil || (*input.After != "" && !configuration.ValidElementID(*input.After)) {
					webResult(w, http.StatusBadRequest, "invalid_request", "")
					return
				}
				result, err = service.Insert(r.Context(), token, csrf, basis, *input.After, json.RawMessage(*input.Source))
			}
		}
		if err != nil {
			webRouterRuleFailure(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			RouterID         string                       `json:"routerId"`
			Name             string                       `json:"name"`
			TemplateID       string                       `json:"templateId"`
			AcceptedVersion  string                       `json:"acceptedVersion"`
			ActiveGeneration string                       `json:"activeGeneration"`
			DraftGeneration  string                       `json:"draftGeneration"`
			Rules            []publication.DraftRuleEntry `json:"rules"`
			PersonalOrder    bool                         `json:"personalOrder"`
		}{
			RouterID: result.Basis.RouterID, Name: result.Name, TemplateID: result.Basis.TemplateID,
			AcceptedVersion:  strconv.FormatInt(result.Basis.AcceptedVersion, 10),
			ActiveGeneration: strconv.FormatInt(result.Basis.ActiveGeneration, 10),
			DraftGeneration:  strconv.FormatInt(result.Basis.DraftGeneration, 10),
			Rules:            result.Rules, PersonalOrder: result.PersonalOrder,
		})
	})
}

func readWebRuleOrder(w http.ResponseWriter, r *http.Request, routerID string) (webadmin.RouterDraftBasis, string, *int, bool) {
	var input struct {
		webRouterDraftInput
		Action   string          `json:"action"`
		RuleID   json.RawMessage `json:"ruleId"`
		Position json.RawMessage `json:"position"`
	}
	if !readWebJSON(w, r, maxWebEditorBody, &input) {
		return webadmin.RouterDraftBasis{}, "", nil, false
	}
	basis, valid := input.basis(routerID)
	var id string
	var position *int
	switch input.Action {
	case "reset":
		valid = valid && input.RuleID == nil && input.Position == nil
	case "move":
		valid = valid && json.Unmarshal(input.RuleID, &id) == nil && configuration.ValidElementID(id) &&
			json.Unmarshal(input.Position, &position) == nil && position != nil && *position >= 0
	default:
		valid = false
	}
	if !valid {
		webResult(w, http.StatusBadRequest, "invalid_request", "")
		return webadmin.RouterDraftBasis{}, "", nil, false
	}
	return basis, id, position, true
}

func webRouterRuleFailure(w http.ResponseWriter, err error) {
	var invalid *publication.LocalRuleSourceError
	var preparation *publication.PreparationError
	switch {
	case errors.As(err, &invalid):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(struct {
			State  string `json:"state"`
			Code   string `json:"code"`
			Offset *int64 `json:"offset,omitempty"`
		}{State: "invalid_source", Code: invalid.Issue.Code, Offset: invalid.Issue.Offset})
	case errors.As(err, &preparation):
		code := ""
		if len(preparation.Issues) != 0 {
			code = preparation.Issues[0].Code
		}
		webResult(w, http.StatusConflict, "order_unavailable", code)
	case errors.Is(err, publication.ErrConflict), errors.Is(err, publication.ErrInvalidCandidate):
		webResult(w, http.StatusConflict, "draft_conflict", "")
	default:
		webRouterVariableFailure(w, err)
	}
}
