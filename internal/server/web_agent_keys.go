package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type webAgentKeys interface {
	Manage(context.Context, string, string, webadmin.AgentKeyCommand) (webadmin.EnrollmentReceipt, error)
}

func NewWebAgentKeysHandler(origin, agentOrigin WebOrigin, service webAgentKeys, admission webPreparationAdmission, audit func(webadmin.AgentKeyAction, string), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/routers/keys" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebEndpoint(w, r, origin, true, http.MethodPost) {
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		ids := query["routerId"]
		if err != nil || len(query) != 1 || len(ids) != 1 || ids[0] == "" || !utf8.ValidString(ids[0]) {
			webResult(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		token, proof := webSessionToken(r), webCSRFProof(r)
		if err := admission.CheckMutation(r.Context(), token, proof); err != nil {
			webFailure(w, err)
			return
		}
		var payload struct {
			Epoch   string                  `json:"epoch"`
			Desired string                  `json:"desiredRevision"`
			Action  webadmin.AgentKeyAction `json:"action"`
		}
		if !readWebJSON(w, r, 1024, &payload) {
			return
		}
		epoch, e1 := strconv.ParseInt(payload.Epoch, 10, 64)
		desired, e2 := strconv.ParseInt(payload.Desired, 10, 64)
		if e1 != nil || e2 != nil || epoch < 0 || desired < 1 || strconv.FormatInt(epoch, 10) != payload.Epoch ||
			strconv.FormatInt(desired, 10) != payload.Desired ||
			(payload.Action != webadmin.RotateAgentKey && payload.Action != webadmin.RecoverAgentKey && payload.Action != webadmin.RevokeAgentKey) {
			webResult(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		if agentOrigin == "" && payload.Action != webadmin.RevokeAgentKey {
			webEnrollmentFailure(w, webadmin.ErrEnrollmentUnavailable)
			return
		}
		receipt, err := service.Manage(r.Context(), token, proof, webadmin.AgentKeyCommand{RouterID: ids[0], Epoch: epoch, Desired: desired, Action: payload.Action})
		if err != nil {
			webEnrollmentFailure(w, err)
			return
		}
		audit(payload.Action, ids[0])
		w.Header().Set("Content-Type", "application/json")
		reply := struct {
			Epoch   string    `json:"epoch"`
			Expires time.Time `json:"expires,omitzero"`
			Token   string    `json:"invitation,omitempty"`
		}{Epoch: strconv.FormatInt(receipt.Epoch, 10)}
		if payload.Action != webadmin.RevokeAgentKey {
			reply.Expires, reply.Token = receipt.Expires, receipt.Token
			w.WriteHeader(http.StatusCreated)
		}
		_ = json.NewEncoder(w).Encode(reply)
	})
}
