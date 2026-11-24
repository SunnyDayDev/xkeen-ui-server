package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/revisionwatch"
)

type AgentWatch interface {
	Wait(context.Context, agentcredentials.Identity, int64) (bool, error)
}

func watchAgent(w http.ResponseWriter, r *http.Request, auth AgentAuthenticator, watch AgentWatch) {
	if !allowAgentMethod(w, r, http.MethodGet) {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	after := query["after"]
	if err != nil || len(query) != 1 || len(after) != 1 || r.ContentLength != 0 {
		agentError(w, 400, "invalid_request")
		return
	}
	number, err := strconv.ParseInt(after[0], 10, 64)
	if err != nil || number < 0 || strconv.FormatInt(number, 10) != after[0] {
		agentError(w, 400, "invalid_request")
		return
	}
	key, ok := agentBearer(r, agentcredential.Working)
	if !ok {
		agentUnauthorized(w)
		return
	}
	id, err := auth.Identity(r.Context(), key)
	if errors.Is(err, agentcredentials.ErrUnauthorized) {
		agentUnauthorized(w)
		return
	}
	if err != nil || id.RouterID == "" || id.Generation < 1 {
		agentError(w, 503, "unavailable")
		return
	}
	changed, err := watch.Wait(r.Context(), id, number)
	if r.Context().Err() != nil {
		return
	}
	switch {
	case errors.Is(err, agentcredentials.ErrUnauthorized):
		agentUnauthorized(w)
	case errors.Is(err, revisionwatch.ErrBusy):
		agentError(w, 429, "watch_busy")
	case err != nil:
		agentError(w, 503, "unavailable")
	case changed:
		writeAgentJSON(w, 200, struct {
			Kind string `json:"kind"`
		}{"desired_changed"})
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	}
}
