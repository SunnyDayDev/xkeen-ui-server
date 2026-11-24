package server

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

type ContactService interface {
	Accept(context.Context, agentcredentials.Identity, *reporting.Report) error
	Cursor(context.Context, agentcredentials.Identity) (int64, error)
}

func agentContact(w http.ResponseWriter, r *http.Request, auth AgentAuthenticator, contacts ContactService) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		agentError(w, 405, "method_not_allowed")
		return
	}
	key, ok := agentBearer(r, agentcredential.Working)
	if !ok {
		agentUnauthorized(w)
		return
	}
	id, err := auth.Identity(r.Context(), key)
	if err != nil {
		contactError(w, err)
		return
	}
	if r.Method == http.MethodGet {
		if r.ContentLength != 0 {
			agentError(w, 400, "invalid_request")
			return
		}
		sequence, err := contacts.Cursor(r.Context(), id)
		if err != nil {
			contactError(w, err)
			return
		}
		writeAgentJSON(w, 200, reporting.Cursor{Sequence: sequence})
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, reporting.MaxBody))
	var oversized *http.MaxBytesError
	if errors.As(err, &oversized) {
		agentError(w, 413, "request_too_large")
		return
	}
	if err != nil {
		agentError(w, 400, "invalid_request")
		return
	}
	var report *reporting.Report
	if len(raw) > 0 {
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		var body struct {
			Report json.RawMessage `json:"report"`
		}
		if err != nil || media != "application/json" || jsonv2.Unmarshal(raw, &body, jsonv2.RejectUnknownMembers(true)) != nil {
			agentError(w, 400, "invalid_request")
			return
		}
		report, err = reporting.DecodeReport(body.Report)
		if err != nil {
			contactError(w, err)
			return
		}
	}
	if err := contacts.Accept(r.Context(), id, report); err != nil {
		contactError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(204)
}

func contactError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agentcredentials.ErrUnauthorized):
		agentUnauthorized(w)
	case errors.Is(err, reporting.ErrInvalid):
		agentError(w, 400, "invalid_request")
	case errors.Is(err, reporting.ErrConflict):
		agentError(w, 409, "report_conflict")
	case errors.Is(err, reporting.ErrUnknownRevision):
		agentError(w, 422, "revision_unknown")
	default:
		agentError(w, 503, "unavailable")
	}
}
