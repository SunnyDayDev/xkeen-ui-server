package localvalidation

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/restart"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

const applicationEndpoint = "/v1/agent-apply"
const applicationRole = "xkeen-local-agent-apply-v1"
const maxApplicationRequest = 4096

type applicationRequest struct {
	Binding    binding
	Credential string
}

type applicationResponse struct {
	Binding binding
	Outcome string
}

type Applier interface {
	Apply(context.Context, string, string) (restart.Confirmed, error)
}

func ApplicationHandler(scope preparation.Scope, apply Applier) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req applicationRequest
		reader := http.MaxBytesReader(w, r.Body, maxApplicationRequest)
		if r.Method != http.MethodPost || r.URL.Path != applicationEndpoint || r.URL.RawQuery != "" || r.URL.ForceQuery ||
			r.Header.Get("Content-Type") != "application/json" || r.ContentLength > maxApplicationRequest ||
			!readJSON(reader, &req) || req.Binding.Role != applicationRole || req.Binding.Action != "apply" ||
			req.Binding.Scope != scope || !agentrevision.ValidTag(req.Binding.Tag) || !validNonce(req.Binding.Nonce) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, ok := agentcredential.Parse(req.Credential, agentcredential.Working); !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 65*time.Second)
		defer cancel()
		_, err := apply.Apply(ctx, req.Credential, req.Binding.Tag)
		reply := applicationResponse{Binding: req.Binding, Outcome: "ok"}
		switch {
		case errors.Is(err, restart.ErrPending):
			reply.Outcome = "pending"
		case errors.Is(err, restart.ErrDesired):
			reply.Outcome = "desired"
		case errors.Is(err, restart.ErrBaseline):
			reply.Outcome = "baseline"
		case errors.Is(err, preparation.ErrRejected):
			reply.Outcome = "candidate"
		case errors.Is(err, restart.ErrJournal):
			reply.Outcome = "journal"
		case errors.Is(err, restart.ErrEffects):
			reply.Outcome = "restart"
		case err != nil:
			reply.Outcome = "unavailable"
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(reply)
	})
}

func ApplyCurrent(ctx context.Context, socket string, scope preparation.Scope, key, tag string) error {
	transport, err := newTransport(socket)
	if err != nil {
		return preparation.ErrUnavailable
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 70 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var nonce [16]byte
	rand.Read(nonce[:])
	bind := binding{Role: applicationRole, Action: "apply", Scope: scope, Tag: tag, Nonce: hex.EncodeToString(nonce[:])}
	data, err := json.Marshal(applicationRequest{Binding: bind, Credential: key})
	if err != nil || len(data) > maxApplicationRequest {
		return preparation.ErrUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+applicationEndpoint, bytes.NewReader(data))
	if err != nil {
		return preparation.ErrUnavailable
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return preparation.ErrUnavailable
	}
	defer response.Body.Close()
	var reply applicationResponse
	if response.StatusCode != http.StatusOK || !readJSON(response.Body, &reply) || reply.Binding != bind {
		return preparation.ErrUnavailable
	}
	switch reply.Outcome {
	case "ok":
		return nil
	case "pending":
		return restart.ErrPending
	case "desired":
		return restart.ErrDesired
	case "baseline":
		return restart.ErrBaseline
	case "candidate":
		return preparation.ErrRejected
	case "journal":
		return restart.ErrJournal
	case "restart":
		return restart.ErrEffects
	default:
		return preparation.ErrUnavailable
	}
}
