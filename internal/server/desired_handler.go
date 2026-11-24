package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/agentcredentials"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
)

type DesiredRevisionReader interface {
	ReadDesired(context.Context, string) (*publication.Revision, error)
}

func readAgentDesired(w http.ResponseWriter, r *http.Request, authenticator AgentAuthenticator, desired DesiredRevisionReader) {
	if !allowAgentMethod(w, r, http.MethodGet) {
		return
	}
	if r.ContentLength != 0 {
		agentError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, ok := agentBearer(r, agentcredential.Working)
	if !ok {
		agentUnauthorized(w)
		return
	}
	id, err := authenticator.Identity(r.Context(), key)
	if errors.Is(err, agentcredentials.ErrUnauthorized) {
		agentUnauthorized(w)
		return
	}
	if err != nil || id.RouterID == "" || id.Generation < 1 || desired == nil {
		agentError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	revision, err := desired.ReadDesired(r.Context(), id.RouterID)
	if err != nil {
		agentError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store, no-transform")
	if revision == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	tag, body, err := agentrevision.Encode(id.RouterID, agentrevision.Snapshot{
		Number: revision.Number, Profile: revision.Catalog.Profile, Files: revision.Catalog.Files,
	})
	if err != nil {
		agentError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	w.Header().Set("ETag", tag)
	conditions := r.Header.Values("If-None-Match")
	if len(conditions) == 1 && conditions[0] == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(20 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		w.Header().Del("ETag")
		agentError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	defer controller.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
