// Package agenthttp implements the agent's pinned HTTPS transport for D03.
package agenthttp

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
)

const maxResponse = 64 * 1024

type Client struct {
	origin string
	http   *http.Client
}

func New(rawOrigin string, roots *x509.CertPool) (*Client, error) {
	parsed, err := url.Parse(rawOrigin)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" || parsed.RawQuery != "" ||
		parsed.ForceQuery || strings.Contains(rawOrigin, "#") || strings.HasSuffix(parsed.Host, ":") {
		return nil, enrollment.ErrOrigin
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, enrollment.ErrOrigin
		}
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &Client{origin: "https://" + parsed.Host, http: client}, nil
}

func (c *Client) Origin() string { return c.origin }

func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }

func (c *Client) Enroll(ctx context.Context, invitation, key string) (string, error) {
	return c.EnrollForRouter(ctx, invitation, key, "")
}

func (c *Client) EnrollForRouter(ctx context.Context, invitation, key, expectedRouter string) (string, error) {
	payload, err := json.Marshal(struct {
		Credential string `json:"credential"`
		RouterID   string `json:"routerId,omitempty"`
	}{key, expectedRouter})
	if err != nil {
		return "", enrollment.ErrProtocol
	}
	return c.exchange(ctx, http.MethodPost, "/agent/v1/enroll", invitation, bytes.NewReader(payload))
}

func (c *Client) Identity(ctx context.Context, key string) (string, error) {
	return c.exchange(ctx, http.MethodGet, "/agent/v1/identity", key, nil)
}

func (c *Client) exchange(ctx context.Context, method, path, token string, body io.Reader) (string, error) {
	requestContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, method, c.origin+path, body)
	if err != nil {
		return "", enrollment.ErrProtocol
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return "", requestError(ctx, err)
	}
	defer response.Body.Close()
	if response.TLS == nil || len(response.TLS.VerifiedChains) == 0 {
		return "", enrollment.ErrTrust
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return "", enrollment.ErrProtocol
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil {
		return "", requestError(ctx, err)
	}
	if len(raw) > maxResponse {
		return "", enrollment.ErrProtocol
	}
	if response.StatusCode == http.StatusOK {
		var payload struct {
			RouterID string `json:"routerId"`
		}
		if jsonv2.Unmarshal(raw, &payload, jsonv2.RejectUnknownMembers(true)) != nil || payload.RouterID == "" {
			return "", enrollment.ErrProtocol
		}
		return payload.RouterID, nil
	}
	return "", decodedFailure(response.StatusCode, raw)
}

func requestError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var verification *tls.CertificateVerificationError
	var invalidRecord tls.RecordHeaderError
	if errors.As(err, &verification) || errors.As(err, &invalidRecord) {
		return enrollment.ErrTrust
	}
	return enrollment.ErrTemporary
}

func decodedFailure(status int, raw []byte) error {
	var payload struct {
		Code string `json:"code"`
	}
	if jsonv2.Unmarshal(raw, &payload, jsonv2.RejectUnknownMembers(true)) != nil {
		return enrollment.ErrProtocol
	}
	switch {
	case status == http.StatusUnauthorized && payload.Code == "unauthorized":
		return enrollment.ErrUnauthorized
	case status == http.StatusServiceUnavailable && payload.Code == "unavailable":
		return enrollment.ErrTemporary
	default:
		return enrollment.ErrProtocol
	}
}
