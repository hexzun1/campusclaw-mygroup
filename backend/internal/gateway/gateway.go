// Package gateway talks to the OpenAI-compatible embedding and chat gateways.
//
// Both are called from the server only; their addresses and keys come from
// environment variables and never reach a browser (spec: 外部服务凭据与访问边界).
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"campusclaw/backend/internal/upstream"
)

// maxErrorBodyBytes bounds how much of an error response gets drained before
// the connection is reused. Its content is never surfaced.
const maxErrorBodyBytes = 4 << 10

// Client performs JSON POSTs against one gateway base URL with a fixed timeout
// (GATEWAY_TIMEOUT_SECONDS).
type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// NewClient builds a gateway client. A non-positive timeout falls back to 30
// seconds so a misconfigured environment cannot hang a request forever.
func NewClient(baseURL, apiKey, model string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		http:    &http.Client{Timeout: timeout},
	}
}

// Model is the model name sent with every request.
func (c *Client) Model() string { return c.model }

// post sends payload as JSON to path and decodes the 2xx body into out.
//
// Every failure mode — transport error, timeout, non-2xx status, undecodable
// body — becomes a sanitized ErrUnavailable, because a caller cannot act
// differently on them and the client must not learn the upstream address.
func (c *Client) post(ctx context.Context, path string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return upstream.Unavailable("could not encode the gateway request", err)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return upstream.Unavailable("could not build the gateway request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return upstream.Unavailable("the gateway is unreachable", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodyBytes))
		return upstream.Unavailable(fmt.Sprintf("the gateway answered HTTP %d", resp.StatusCode), nil)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return upstream.Unavailable("the gateway response could not be decoded", err)
	}
	return nil
}
