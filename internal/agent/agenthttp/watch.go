package agenthttp

import (
	"context"
	jsonv2 "encoding/json/v2"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
)

func (c *Client) Watch(ctx context.Context, key string, after int64) (bool, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, c.origin+"/agent/v1/watch?after="+strconv.FormatInt(after, 10), nil)
	if err != nil {
		return false, enrollment.ErrProtocol
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return false, requestError(ctx, err)
	}
	defer response.Body.Close()
	if response.TLS == nil || len(response.TLS.VerifiedChains) == 0 {
		return false, enrollment.ErrTrust
	}
	if response.StatusCode == 204 {
		return false, nil
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return false, enrollment.ErrProtocol
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil {
		return false, requestError(ctx, err)
	}
	if len(raw) > maxResponse {
		return false, enrollment.ErrProtocol
	}
	switch response.StatusCode {
	case 200:
		var reply struct {
			Kind string `json:"kind"`
		}
		if jsonv2.Unmarshal(raw, &reply, jsonv2.RejectUnknownMembers(true)) != nil || reply.Kind != "desired_changed" {
			return false, enrollment.ErrProtocol
		}
		return true, nil
	case 429:
		var reply struct {
			Code string `json:"code"`
		}
		if jsonv2.Unmarshal(raw, &reply, jsonv2.RejectUnknownMembers(true)) != nil || reply.Code != "watch_busy" {
			return false, enrollment.ErrProtocol
		}
		return false, enrollment.ErrTemporary
	default:
		return false, decodedFailure(response.StatusCode, raw)
	}
}
