package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
)

type webRouterEnrollmentService interface {
	Status(context.Context, string, string) (webadmin.EnrollmentState, error)
	Issue(context.Context, string, string, webadmin.EnrollmentCommand) (webadmin.EnrollmentReceipt, error)
}

func NewWebRouterEnrollmentHandler(origin, agentOrigin WebOrigin, service webRouterEnrollmentService, admission webPreparationAdmission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/v1/routers/enroll" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowWebEndpoint(w, r, origin, true, http.MethodGet, http.MethodPost) {
			return
		}
		params, err := url.ParseQuery(r.URL.RawQuery)
		ids := params["routerId"]
		if err != nil || len(params) != 1 || len(ids) != 1 || ids[0] == "" || !utf8.ValidString(ids[0]) {
			webResult(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		token := webSessionToken(r)
		if r.Method == http.MethodGet {
			state, err := service.Status(r.Context(), token, ids[0])
			if err != nil {
				webEnrollmentFailure(w, err)
				return
			}
			writeWebEnrollmentState(w, agentOrigin, state)
			return
		}
		proof := webCSRFProof(r)
		if err := admission.CheckMutation(r.Context(), token, proof); err != nil {
			webFailure(w, err)
			return
		}
		if agentOrigin == "" {
			webEnrollmentFailure(w, webadmin.ErrEnrollmentUnavailable)
			return
		}
		var payload struct {
			Epoch   string `json:"epoch"`
			Desired string `json:"desiredRevision"`
			Action  string `json:"action"`
		}
		if !readWebJSON(w, r, 1024, &payload) {
			return
		}
		epoch, e1 := strconv.ParseInt(payload.Epoch, 10, 64)
		desired, e2 := strconv.ParseInt(payload.Desired, 10, 64)
		if e1 != nil || e2 != nil || epoch < 0 || desired < 1 || strconv.FormatInt(epoch, 10) != payload.Epoch ||
			strconv.FormatInt(desired, 10) != payload.Desired || (payload.Action != "issue" && payload.Action != "replace") {
			webResult(w, http.StatusBadRequest, "invalid_request", "")
			return
		}
		receipt, err := service.Issue(r.Context(), token, proof, webadmin.EnrollmentCommand{
			RouterID: ids[0], Epoch: epoch, Desired: desired, Replace: payload.Action == "replace",
		})
		if err != nil {
			webEnrollmentFailure(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(struct {
			Epoch   string    `json:"epoch"`
			Expires time.Time `json:"expires"`
			Token   string    `json:"invitation"`
		}{strconv.FormatInt(receipt.Epoch, 10), receipt.Expires, receipt.Token})
	})
}

func writeWebEnrollmentState(w http.ResponseWriter, origin WebOrigin, state webadmin.EnrollmentState) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		RouterID string    `json:"routerId"`
		Name     string    `json:"name"`
		Origin   WebOrigin `json:"origin"`
		Phase    string    `json:"phase"`
		Epoch    string    `json:"epoch"`
		Desired  string    `json:"desiredRevision"`
		Expires  time.Time `json:"expires,omitzero"`
		Contact  string    `json:"contact"`
		Applied  string    `json:"appliedRevision,omitempty"`
		Runtime  string    `json:"runtime,omitempty"`
	}{
		RouterID: state.RouterID, Name: state.Name, Origin: origin, Phase: state.Phase,
		Epoch: strconv.FormatInt(state.Epoch, 10), Desired: strconv.FormatInt(state.Desired, 10), Expires: state.Expires,
		Contact: state.Contact, Applied: optionalWebRevision(state.Applied), Runtime: state.Runtime,
	})
}

func optionalWebRevision(number int64) string {
	if number == 0 {
		return ""
	}
	return strconv.FormatInt(number, 10)
}

func webEnrollmentFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, webadmin.ErrInvalidEnrollment):
		webResult(w, http.StatusBadRequest, "invalid_request", "")
	case errors.Is(err, webadmin.ErrEnrollmentMissing):
		webResult(w, http.StatusNotFound, "not_prepared", "")
	case errors.Is(err, webadmin.ErrEnrollmentConflict):
		webResult(w, http.StatusConflict, "conflict", "")
	case errors.Is(err, webadmin.ErrEnrollmentUnavailable):
		webResult(w, http.StatusServiceUnavailable, "enrollment_unavailable", "")
	default:
		webFailure(w, err)
	}
}
