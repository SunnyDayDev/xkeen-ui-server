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
const applicationReportEndpoint = "/v1/agent-report"

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
	return ApplicationProfileHandler(scope, apply, nil)
}

func ApplicationProfileHandler(scope preparation.Scope, apply Applier, report func(context.Context, string) (bool, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reporting := r.URL.Path == applicationReportEndpoint && report != nil
		if !reporting && (r.URL.Path != applicationEndpoint || apply == nil) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		action := "apply"
		if report != nil {
			action = "apply_report_v1"
		}
		if reporting {
			action = "report_v1"
		}
		var req applicationRequest
		reader := http.MaxBytesReader(w, r.Body, maxApplicationRequest)
		if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.ForceQuery ||
			r.Header.Get("Content-Type") != "application/json" || r.ContentLength > maxApplicationRequest ||
			!readJSON(reader, &req) || req.Binding.Role != applicationRole || req.Binding.Action != action ||
			req.Binding.Scope != scope || !validNonce(req.Binding.Nonce) ||
			(reporting && req.Binding.Tag != "") || (!reporting && !agentrevision.ValidTag(req.Binding.Tag)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, ok := agentcredential.Parse(req.Credential, agentcredential.Working); !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 65*time.Second)
		defer cancel()
		var err error
		var delivered bool
		if reporting {
			delivered, err = report(ctx, req.Credential)
		} else {
			_, err = apply.Apply(ctx, req.Credential, req.Binding.Tag)
		}
		reply := applicationResponse{Binding: req.Binding, Outcome: "ok"}
		switch {
		case errors.Is(err, restart.ErrRolledBack):
			reply.Outcome = "rolled_back"
		case errors.Is(err, restart.ErrRollbackFailed):
			reply.Outcome = "rollback_failed"
		case errors.Is(err, restart.ErrReportPending):
			reply.Outcome = "report_pending"
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
		case delivered:
			reply.Outcome = "reported"
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(reply)
	})
}

func ApplyCurrent(ctx context.Context, socket string, scope preparation.Scope, key, tag string) error {
	_, err := requestApplication(ctx, socket, scope, key, tag, applicationEndpoint, "apply")
	return err
}

func ApplyReportable(ctx context.Context, socket string, scope preparation.Scope, key, tag string) error {
	_, err := requestApplication(ctx, socket, scope, key, tag, applicationEndpoint, "apply_report_v1")
	return err
}

func ReconcileReport(ctx context.Context, socket string, scope preparation.Scope, key string) (bool, error) {
	return requestApplication(ctx, socket, scope, key, "", applicationReportEndpoint, "report_v1")
}

func requestApplication(ctx context.Context, socket string, scope preparation.Scope, key, tag, endpoint, action string) (bool, error) {
	transport, err := newTransport(socket)
	if err != nil {
		return false, preparation.ErrUnavailable
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 70 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var nonce [16]byte
	rand.Read(nonce[:])
	bind := binding{Role: applicationRole, Action: action, Scope: scope, Tag: tag, Nonce: hex.EncodeToString(nonce[:])}
	data, err := json.Marshal(applicationRequest{Binding: bind, Credential: key})
	if err != nil || len(data) > maxApplicationRequest {
		return false, preparation.ErrUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+endpoint, bytes.NewReader(data))
	if err != nil {
		return false, preparation.ErrUnavailable
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return false, preparation.ErrUnavailable
	}
	defer response.Body.Close()
	var reply applicationResponse
	if response.StatusCode != http.StatusOK || !readJSON(response.Body, &reply) || reply.Binding != bind {
		return false, preparation.ErrUnavailable
	}
	switch reply.Outcome {
	case "reported":
		if action == "report_v1" {
			return true, nil
		}
		return false, preparation.ErrUnavailable
	case "ok":
		return false, nil
	case "rolled_back":
		return false, restart.ErrRolledBack
	case "rollback_failed":
		return false, restart.ErrRollbackFailed
	case "pending":
		return false, restart.ErrPending
	case "report_pending":
		return false, restart.ErrReportPending
	case "desired":
		return false, restart.ErrDesired
	case "baseline":
		return false, restart.ErrBaseline
	case "candidate":
		return false, preparation.ErrRejected
	case "journal":
		return false, restart.ErrJournal
	case "restart":
		return false, restart.ErrEffects
	default:
		return false, preparation.ErrUnavailable
	}
}
