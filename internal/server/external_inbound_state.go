package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
)

var errInboundStateConflict = errors.New("inbound state cannot be changed")
var errInboundAnalysis = errors.New("inbound dependency analysis unavailable")

func NewExternalInboundState(store ExternalRouterDraftStore, next ExternalOperation) ExternalOperation {
	return externalRouterElementOperation("/api/v1/routers/inbounds", store, externalInboundStateRepresentation, readExternalInboundState, next)
}

func externalInboundStateRepresentation(s routerdraft.Snapshot, id string) ([]byte, string, error) {
	prepared, err := publication.PrepareDraftInbounds(s.Accepted, s.Editable, s.OrderBasis)
	if err != nil {
		return nil, "", inboundStateError(err)
	}
	state, err := prepared.InboundState(id)
	if err != nil {
		return nil, "", inboundStateError(err)
	}
	return externalRouterDocument("external-inbound-state", struct {
		externalRouterBasis
		ElementID string `json:"elementId"`
		publication.DraftInboundState
	}{externalRouterBasisFrom(s.Basis), id, state})
}

func readExternalInboundState(w http.ResponseWriter, r *http.Request) (externalRouterMutation, bool) {
	var input struct {
		Action string `json:"action"`
	}
	if !readExternalTemplateJSON(w, r, 1024, &input) {
		return nil, false
	}
	if input.Action != "disable" && input.Action != "enable" {
		externalError(w, 400, "invalid_request")
		return nil, false
	}
	return func(s routerdraft.Snapshot, id string) (routersettings.Draft, error) {
		prepared, err := publication.PrepareDraftInbounds(s.Accepted, s.Editable, s.OrderBasis)
		if err != nil {
			return routersettings.Draft{}, inboundStateError(err)
		}
		next, err := prepared.SetInboundDisabled(id, input.Action == "disable")
		return next, inboundStateError(err)
	}, true
}

func inboundStateError(err error) error {
	var structure *publication.PreparationError
	switch {
	case errors.As(err, &structure):
		return errInboundAnalysis
	case errors.Is(err, publication.ErrSourceMissing):
		return routerdraft.ErrMissing
	case errors.Is(err, publication.ErrSourceMismatch):
		return routerdraft.ErrForeign
	case errors.Is(err, publication.ErrConflict), errors.Is(err, publication.ErrInvalidCandidate):
		return errInboundStateConflict
	default:
		return err
	}
}

func writeInboundStateFailure(w http.ResponseWriter, err error) bool {
	var used *publication.InboundInUse
	switch {
	case errors.Is(err, errInboundAnalysis):
		externalError(w, 409, "analysis_unavailable")
	case errors.Is(err, errInboundStateConflict):
		externalError(w, 409, "state_conflict")
	case errors.As(err, &used):
		refs := make([]publication.DraftIOBlocker, 0, len(used.Dependencies))
		for _, dep := range used.Dependencies {
			refs = append(refs, publication.DraftIOBlocker{RuleID: dep.RuleID, Kind: "inbound", Path: dep.Path})
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(struct {
			Code       string                       `json:"code"`
			References []publication.DraftIOBlocker `json:"references"`
		}{"inbound_in_use", refs})
	default:
		return false
	}
	return true
}
