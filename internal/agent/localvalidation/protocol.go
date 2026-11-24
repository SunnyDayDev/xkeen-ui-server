// Package localvalidation connects the agent to its separately trusted local supervisor.
package localvalidation

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

const role = "xkeen-local-agent-check-v1"
const endpoint = "/v1/agent-check"
const maxRequest = agentrevision.MaxBody + 128<<10

type binding struct {
	Role, Action, Nonce, Tag string
	Scope                    preparation.Scope
}

type request struct {
	Binding  binding
	Snapshot json.RawMessage `json:",omitempty"`
}

type response struct {
	Binding  binding
	Outcome  string
	Baseline *preparation.Baseline `json:",omitempty"`
}

type Client struct {
	http  *http.Client
	scope preparation.Scope
}

func NewClient(path string, scope preparation.Scope) (*Client, error) {
	transport, err := newTransport(path)
	if err != nil {
		return nil, preparation.ErrUnavailable
	}
	return &Client{
		http:  &http.Client{Transport: transport, Timeout: 18 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		scope: scope,
	}, nil
}

func (c *Client) Observe(ctx context.Context) (preparation.Baseline, error) {
	result, err := c.send(ctx, "observe", "", nil)
	if err != nil || result.Outcome != "ok" || result.Baseline == nil {
		return preparation.Baseline{}, preparation.ErrBaseline
	}
	return *result.Baseline, nil
}

func (c *Client) Check(ctx context.Context, scope preparation.Scope, snapshot agentrevision.Snapshot) error {
	if scope != c.scope {
		return preparation.ErrUnavailable
	}
	tag, body, err := agentrevision.Encode(scope.RouterID, snapshot)
	if err != nil {
		return preparation.ErrUnavailable
	}
	result, err := c.send(ctx, "check", tag, body)
	if err != nil || result.Baseline != nil {
		return preparation.ErrUnavailable
	}
	switch result.Outcome {
	case "ok":
		return nil
	case "rejected":
		return preparation.ErrRejected
	default:
		return preparation.ErrUnavailable
	}
}

func (c *Client) send(ctx context.Context, action, tag string, body []byte) (response, error) {
	var nonce [16]byte
	rand.Read(nonce[:])
	bind := binding{Role: role, Action: action, Nonce: hex.EncodeToString(nonce[:]), Tag: tag, Scope: c.scope}
	raw, err := json.Marshal(request{Binding: bind, Snapshot: body})
	if err != nil || len(raw) > maxRequest {
		return response{}, preparation.ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+endpoint, bytes.NewReader(raw))
	if err != nil {
		return response{}, preparation.ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	got, err := c.http.Do(req)
	if err != nil {
		return response{}, preparation.ErrUnavailable
	}
	defer got.Body.Close()
	var result response
	if got.StatusCode != http.StatusOK || !readJSON(got.Body, &result) || result.Binding != bind {
		return response{}, preparation.ErrUnavailable
	}
	return result, nil
}

type JobRunner interface {
	Run(context.Context, string, agentrevision.Snapshot) error
}

func Handler(scope preparation.Scope, observer preparation.Observer, runner JobRunner) http.Handler {
	gate := make(chan struct{}, 1)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != endpoint || r.URL.RawQuery != "" || r.URL.ForceQuery ||
			r.Header.Get("Content-Type") != "application/json" || r.ContentLength > maxRequest {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var req request
		if !readJSON(r.Body, &req) || req.Binding.Role != role || req.Binding.Scope != scope || !validNonce(req.Binding.Nonce) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		reply := response{Binding: req.Binding, Outcome: "unavailable"}
		switch req.Binding.Action {
		case "observe":
			if len(req.Snapshot) != 0 || req.Binding.Tag != "" {
				break
			}
			value, err := observer.Observe(r.Context())
			if err == nil {
				reply.Outcome, reply.Baseline = "ok", &value
			}
		case "check":
			snapshot, err := agentrevision.Decode(scope.RouterID, req.Binding.Tag, req.Snapshot)
			if err != nil || snapshot.Profile != scope.Profile {
				break
			}
			switch err := runner.Run(r.Context(), scope.RouterID, *snapshot); {
			case err == nil:
				reply.Outcome = "ok"
			case errors.Is(err, preparation.ErrRejected):
				reply.Outcome = "rejected"
			}
		}
		if r.Context().Err() != nil {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(reply)
	})
}

func readJSON(r io.Reader, dst any) bool {
	raw, err := io.ReadAll(io.LimitReader(r, maxRequest+1))
	return err == nil && len(raw) <= maxRequest && jsonv2.Unmarshal(raw, dst, jsonv2.RejectUnknownMembers(true)) == nil
}

func validNonce(s string) bool {
	if len(s) != 32 {
		return false
	}
	raw, err := hex.DecodeString(s)
	return err == nil && s == hex.EncodeToString(raw)
}
