package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/externalaccess"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
)

func NewExternalProfileSwitch(lifetime context.Context, svc externalaccess.ProfileSwitches, next ExternalOperation) ExternalOperation {
	return func(w http.ResponseWriter, r *http.Request, permit externalaccess.Permit) {
		if r.URL.Path != "/api/v1/routers/profile-switch" {
			next(w, r, permit)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, POST")
			externalError(w, 405, "method_not_allowed")
			return
		}
		q, err := url.ParseQuery(r.URL.RawQuery)
		id, target := q.Get("routerId"), q.Get("templateId")
		if err != nil || len(q) != 2 || len(q["routerId"]) != 1 || len(q["templateId"]) != 1 || id == "" || target == "" || !utf8.ValidString(id) || !utf8.ValidString(target) {
			externalError(w, 400, "invalid_request")
			return
		}
		ctx, done := publicationRequestContext(r.Context(), lifetime)
		defer done()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			current, err := svc.Read(ctx, permit, id, target)
			if err != nil {
				externalSwitchFailure(w, err)
				return
			}
			fields, err := externalaccess.ProfileSwitchFields(current)
			if err != nil {
				externalSwitchFailure(w, err)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"routerId": id, "activeTemplateId": current.Basis.SourceTemplateID, "targetTemplateId": target, "alreadyActive": current.AlreadyActive, "restoring": current.TargetDraft != nil, "desiredRevision": strconv.FormatInt(current.Basis.DesiredNumber, 10), "fields": fields})
			return
		}
		var cmd struct {
			Action  string          `json:"action"`
			Receipt json.RawMessage `json:"receipt"`
			Values  []struct {
				ElementID string          `json:"elementId"`
				Name      string          `json:"name"`
				Source    json.RawMessage `json:"source"`
			} `json:"values"`
		}
		if !readExternalPublicationJSON(w, r, maxWebEditorBody, &cmd) {
			return
		}
		if cmd.Values == nil {
			externalError(w, 400, "invalid_request")
			return
		}
		var receiptText string
		req := publication.ProfileSwitchRequest{RouterID: id, TargetTemplateID: target}
		for _, v := range cmd.Values {
			var source *string
			if len(v.Source) == 0 || json.Unmarshal(v.Source, &source) != nil {
				externalError(w, 400, "invalid_request")
				return
			}
			req.Values = append(req.Values, publication.ProfileVariableEdit{ElementID: v.ElementID, Name: v.Name, Source: source})
		}
		switch {
		case cmd.Action == "preview" && len(cmd.Receipt) == 0:
			preview, receipt, err := svc.Preview(ctx, permit, req)
			if err != nil {
				externalSwitchFailure(w, err)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"routerId": id, "activeTemplateId": preview.Proof.Basis.SourceTemplateID, "targetTemplateId": target, "restoring": preview.Restoring, "changed": preview.Changed, "files": preview.Changes, "receipt": receipt})
		case cmd.Action == "confirm" && json.Unmarshal(cmd.Receipt, &receiptText) == nil && receiptText != "":
			result, err := svc.Confirm(ctx, permit, req, receiptText)
			if err != nil {
				externalSwitchFailure(w, err)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"routerId": id, "activeTemplateId": target, "desiredRevision": strconv.FormatInt(result.Revision, 10), "changed": result.Changed})
		default:
			externalError(w, 400, "invalid_request")
		}
	}
}
func externalSwitchFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, publication.ErrEffectRefused):
		externalFailure(w, externalaccess.ErrUnauthorized)
	case errors.Is(err, publication.ErrProfileAlreadyActive):
		externalError(w, 409, "already_active")
	case errors.Is(err, publication.ErrInvalidCommand):
		externalError(w, 400, "invalid_request")
	default:
		externalTemplatePublicationFailure(w, err)
	}
}
