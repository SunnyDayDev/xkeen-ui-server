package agenthttp

import (
	"bytes"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/reporting"
)

func (c *Client) Cursor(ctx context.Context, key string) (int64, error) {
	raw, err := c.contactRequest(ctx, http.MethodGet, key, nil, http.StatusOK)
	if err != nil {
		return 0, err
	}
	var out struct {
		Sequence string `json:"sequence"`
	}
	if jsonv2.Unmarshal(raw, &out, jsonv2.RejectUnknownMembers(true)) != nil {
		return 0, enrollment.ErrProtocol
	}
	seq, err := strconv.ParseInt(out.Sequence, 10, 64)
	if err != nil || seq < 0 || strconv.FormatInt(seq, 10) != out.Sequence {
		return 0, enrollment.ErrProtocol
	}
	return seq, nil
}
func (c *Client) Contact(ctx context.Context, key string, r *reporting.Report) error {
	var body []byte
	if r != nil {
		var err error
		body, err = json.Marshal(struct {
			Report *reporting.Report `json:"report"`
		}{r})
		if err != nil {
			return enrollment.ErrProtocol
		}
	}
	_, err := c.contactRequest(ctx, http.MethodPost, key, body, http.StatusNoContent)
	return err
}
func (c *Client) contactRequest(ctx context.Context, method, key string, body []byte, success int) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, c.origin+"/agent/v1/contact", bytes.NewReader(body))
	if err != nil {
		return nil, enrollment.ErrProtocol
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, requestError(ctx, err)
	}
	defer res.Body.Close()
	if res.TLS == nil || len(res.TLS.VerifiedChains) == 0 {
		return nil, enrollment.ErrTrust
	}
	if res.StatusCode == http.StatusNoContent && success == http.StatusNoContent {
		return nil, nil
	}
	media, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return nil, enrollment.ErrProtocol
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, reporting.MaxBody+1))
	if err != nil {
		return nil, requestError(ctx, err)
	}
	if len(raw) > reporting.MaxBody {
		return nil, enrollment.ErrProtocol
	}
	if res.StatusCode == success {
		return raw, nil
	}
	var failure struct {
		Code string `json:"code"`
	}
	if jsonv2.Unmarshal(raw, &failure, jsonv2.RejectUnknownMembers(true)) != nil {
		return nil, enrollment.ErrProtocol
	}
	if res.StatusCode == 409 && failure.Code == "report_conflict" {
		return nil, reporting.ErrConflict
	}
	if res.StatusCode == 422 && failure.Code == "revision_unknown" {
		return nil, reporting.ErrUnknownRevision
	}
	return nil, decodedFailure(res.StatusCode, raw)
}
