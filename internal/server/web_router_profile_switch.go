package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type webProfileSwitchCommand struct {
	Action  string          `json:"action"`
	Target  string          `json:"targetTemplateId"`
	Receipt json.RawMessage `json:"receipt"`
	Values  []struct {
		ElementID string          `json:"elementId"`
		Name      string          `json:"name"`
		Source    json.RawMessage `json:"source"`
	} `json:"values"`
}

func NewWebRouterProfileSwitchHandler(origin WebOrigin, service *webadmin.RouterProfileSwitch, sessions *webadmin.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/routers/profile" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebEndpoint(w, r, origin, true, http.MethodGet, http.MethodPost) {
			return
		}
		q, err := url.ParseQuery(r.URL.RawQuery)
		id, target := q["routerId"], q["targetTemplateId"]
		get := r.Method == http.MethodGet
		if err != nil || !oneProfileID(id) || get && (len(q) != 2 || !oneProfileID(target)) || !get && len(q) != 1 {
			webResult(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		token := webSessionToken(r)
		if get {
			result, err := service.Read(r.Context(), token, id[0], target[0])
			writeProfileSwitchReply(w, result, err)
			return
		}
		csrf := webCSRFProof(r)
		if err := sessions.CheckMutation(r.Context(), token, csrf); err != nil {
			webFailure(w, err)
			return
		}
		var payload webProfileSwitchCommand
		if !readWebJSON(w, r, maxWebEditorBody, &payload) {
			return
		}
		request := publication.ProfileSwitchRequest{RouterID: id[0], TargetTemplateID: payload.Target}
		var receipt string
		receiptValid := json.Unmarshal(payload.Receipt, &receipt) == nil && receipt != ""
		if payload.Target == "" || payload.Values == nil || payload.Action != "preview" && payload.Action != "confirm" ||
			payload.Action == "preview" && payload.Receipt != nil || payload.Action == "confirm" && !receiptValid {
			webResult(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		for _, field := range payload.Values {
			var source *string
			if field.ElementID == "" || field.Name == "" || json.Unmarshal(field.Source, &source) != nil {
				webResult(w, http.StatusBadRequest, "invalid_request", "")
				return
			}
			request.Values = append(request.Values, publication.ProfileVariableEdit{ElementID: field.ElementID, Name: field.Name, Source: source})
		}
		if payload.Action == "preview" {
			result, err := service.Preview(r.Context(), token, csrf, request)
			writeProfileSwitchReply(w, result, err)
			return
		}
		result, err := service.Confirm(r.Context(), token, csrf, request, receipt)
		if err != nil {
			writeProfileSwitchReply(w, nil, err)
			return
		}
		writeProfileSwitchReply(w, struct {
			TemplateID      string `json:"templateId"`
			DesiredRevision string `json:"desiredRevision"`
			Changed         bool   `json:"changed"`
		}{request.TargetTemplateID, strconv.FormatInt(result.Revision, 10), result.Changed}, nil)
	})
}

func oneProfileID(values []string) bool {
	return len(values) == 1 && values[0] != "" && utf8.ValidString(values[0])
}

func writeProfileSwitchReply(w http.ResponseWriter, result any, err error) {
	var prepared *publication.PreparationError
	switch {
	case err == nil:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	case errors.Is(err, publication.ErrSourceMissing):
		webResult(w, http.StatusNotFound, "scope_missing", "")
	case errors.Is(err, publication.ErrConflict), errors.Is(err, publication.ErrProfileAlreadyActive), errors.Is(err, publication.ErrProfileSwitchReceipt):
		webResult(w, http.StatusConflict, "scope_changed", "")
	case errors.Is(err, publication.ErrCatalogRejected):
		webResult(w, http.StatusUnprocessableEntity, "catalog_rejected", "")
	case errors.As(err, &prepared):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(struct {
			State  string                    `json:"state"`
			Issues []routerCatalogDiagnostic `json:"issues"`
		}{"candidate_invalid", routerCatalogDiagnostics(prepared)})
	case errors.Is(err, publication.ErrInvalidCommand):
		webResult(w, http.StatusBadRequest, "invalid_request", "")
	case errors.Is(err, publication.ErrValidatorUnavailable):
		webResult(w, http.StatusServiceUnavailable, "validator_unavailable", "")
	case errors.Is(err, publication.ErrBypassUnavailable):
		webResult(w, http.StatusUnprocessableEntity, "bypass_unavailable", "")
	case errors.Is(err, publication.ErrInvalidCandidate):
		webResult(w, http.StatusUnprocessableEntity, "candidate_invalid", "")
	default:
		webFailure(w, err)
	}
}
