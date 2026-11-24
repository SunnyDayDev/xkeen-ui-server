package agenthttp

import (
	"context"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/desired"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
)

func (c *Client) ReadDesired(ctx context.Context, key, tag string) (desired.Response, error) {
	requestContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, c.origin+"/agent/v1/desired", nil)
	if err != nil {
		return desired.Response{}, enrollment.ErrProtocol
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	if tag != "" {
		request.Header.Set("If-None-Match", tag)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return desired.Response{}, requestError(ctx, err)
	}
	defer response.Body.Close()
	if response.TLS == nil || len(response.TLS.VerifiedChains) == 0 {
		return desired.Response{}, enrollment.ErrTrust
	}
	if response.Header.Get("Content-Encoding") != "" {
		return desired.Response{}, enrollment.ErrProtocol
	}
	var reply desired.Response
	tags := response.Header.Values("ETag")
	if len(tags) == 1 {
		reply.Tag = tags[0]
	}
	switch response.StatusCode {
	case http.StatusNoContent:
		if len(tags) != 0 {
			return desired.Response{}, enrollment.ErrProtocol
		}
		reply.Kind = desired.Absent
		return reply, nil
	case http.StatusNotModified:
		reply.Kind = desired.NotModified
		return reply, nil
	}

	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return desired.Response{}, enrollment.ErrProtocol
	}
	limit := int64(maxResponse)
	if response.StatusCode == http.StatusOK {
		limit = agentrevision.MaxBody
	}
	if response.ContentLength > limit {
		return desired.Response{}, enrollment.ErrProtocol
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return desired.Response{}, requestError(ctx, err)
	}
	if int64(len(body)) > limit || (response.ContentLength >= 0 && response.ContentLength != int64(len(body))) {
		return desired.Response{}, enrollment.ErrProtocol
	}
	if response.StatusCode != http.StatusOK {
		return desired.Response{}, decodedFailure(response.StatusCode, body)
	}
	reply.Kind, reply.Body = desired.Complete, body
	return reply, nil
}
