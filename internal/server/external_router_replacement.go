package server

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"net/http"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routerdraft"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/routersettings"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

const externalReplacementPath = "/api/v1/routers/replacement"

var errExternalReplacementMode = errors.New("replacement transition not available")

func NewExternalRouterReplacement(store ExternalRouterDraftStore, next ExternalOperation) ExternalOperation {
	return externalRouterElementOperation(externalReplacementPath, store, externalReplacementRepresentation, readExternalReplacementPatch, next)
}

func readExternalReplacementPatch(w http.ResponseWriter, r *http.Request) (externalRouterMutation, bool) {
	var input struct {
		Action  configuration.ContentActionKind `json:"action"`
		Source  json.RawMessage                 `json:"source"`
		Confirm json.RawMessage                 `json:"confirm"`
	}
	if !readExternalTemplateJSON(w, r, maxWebEditorBody, &input) {
		return nil, false
	}
	action := configuration.ContentAction{Kind: input.Action}
	switch input.Action {
	case configuration.ActivateReplacement:
		var source *string
		if input.Confirm == nil && jsonv2.Unmarshal(input.Source, &source) == nil && source != nil {
			action.Content = json.RawMessage(*source)
			break
		}
		externalError(w, http.StatusBadRequest, "invalid_request")
		return nil, false
	case configuration.ReturnToVariables, configuration.ResetContentOverride:
		var confirmed bool
		if input.Source == nil && jsonv2.Unmarshal(input.Confirm, &confirmed) == nil && confirmed {
			break
		}
		fallthrough
	default:
		externalError(w, http.StatusBadRequest, "invalid_request")
		return nil, false
	}
	return func(snapshot routerdraft.Snapshot, id string) (routersettings.Draft, error) {
		draft, err := routerdraft.EditReplacement(snapshot, id, action)
		if errors.Is(err, routersettings.ErrVariableMode) {
			return routersettings.Draft{}, errExternalReplacementMode
		}
		return draft, err
	}, true
}

func externalReplacementRepresentation(snapshot routerdraft.Snapshot, id string) ([]byte, string, error) {
	selection, err := routerdraft.InspectReplacement(snapshot, id)
	if err != nil {
		return nil, "", err
	}
	return externalRouterDocument("external-router-replacement", struct {
		externalRouterBasis
		routerdraft.ReplacementSelection
	}{externalRouterBasisFrom(snapshot.Basis), selection})
}
