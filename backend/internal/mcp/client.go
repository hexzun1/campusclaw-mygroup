// Package mcp is the host-side client for externally registered MCP tool
// servers. It speaks only the subset the product needs: Streamable HTTP with
// a single JSON response (no SSE), initialize handshake, tools/list and
// tools/call (design.md Decision 7).
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const protocolVersion = "2025-06-18"

// Tool is one discovered tool. InputSchema stays raw JSON: the host only needs
// `required` and basic types, and the teacher page shows it verbatim.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// CallResult is the cleaned outcome of one tools/call. Only text content is
// kept; the host truncates and strips control characters later.
type CallResult struct {
	Text    string
	IsError bool
}

// Client talks to every registered server. Timeouts and the response cap come
// from configuration so they stay testable and adjustable.
type Client struct {
	HTTPClient       *http.Client
	ConnectTimeout   time.Duration
	CallTimeout      time.Duration
	MaxResponseBytes int64
}

func NewClient(connectTimeout, callTimeout time.Duration, maxResponseBytes int64) *Client {
	return &Client{
		HTTPClient:       &http.Client{},
		ConnectTimeout:   connectTimeout,
		CallTimeout:      callTimeout,
		MaxResponseBytes: maxResponseBytes,
	}
}

// Discover performs initialize → notifications/initialized → tools/list within
// the connect timeout.
func (c *Client) Discover(ctx context.Context, baseURL, token string) ([]Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.ConnectTimeout)
	defer cancel()

	s, err := c.handshake(ctx, baseURL, token)
	if err != nil {
		return nil, redactError(err, token)
	}

	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.call(ctx, baseURL, token, s, "tools/list", nil, &result); err != nil {
		return nil, redactError(err, token)
	}
	for i := range result.Tools {
		if len(result.Tools[i].InputSchema) == 0 || string(result.Tools[i].InputSchema) == "null" {
			result.Tools[i].InputSchema = json.RawMessage(`{}`)
		}
	}
	return result.Tools, nil
}

// Call performs a lightweight handshake and then tools/call within the call
// timeout. Servers are stateless, so a handshake per call is the compatible
// and simple choice (design.md Decision 7).
func (c *Client) Call(ctx context.Context, baseURL, token, tool string, arguments json.RawMessage) (CallResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.CallTimeout)
	defer cancel()

	s, err := c.handshake(ctx, baseURL, token)
	if err != nil {
		return CallResult{}, redactError(err, token)
	}

	params := map[string]any{"name": tool, "arguments": arguments}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := c.call(ctx, baseURL, token, s, "tools/call", params, &result); err != nil {
		return CallResult{}, redactError(err, token)
	}

	var texts []string
	for _, item := range result.Content {
		if item.Type == "text" {
			texts = append(texts, item.Text)
		}
	}
	// A remote server can echo the token it was called with, and this text
	// reaches the model through tool messages and the user message. The token
	// is redacted here, before the host truncates: a cut in the middle of a
	// token would otherwise leave half of it behind (design.md Decision 4).
	text := redact(strings.Join(texts, "\n"), token)
	return CallResult{Text: text, IsError: result.IsError}, nil
}

type session struct {
	version   string
	sessionID string
}

func (c *Client) handshake(ctx context.Context, baseURL, token string) (*session, error) {
	s := &session{version: protocolVersion}
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "campusclaw-api", "version": "1.0"},
	}
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := c.call(ctx, baseURL, token, s, "initialize", params, &result); err != nil {
		return nil, err
	}
	if result.ProtocolVersion != "" {
		s.version = result.ProtocolVersion
	}

	if err := c.notify(ctx, baseURL, token, s, "notifications/initialized"); err != nil {
		return nil, err
	}
	return s, nil
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// call sends one JSON-RPC request and decodes its result into out.
func (c *Client) call(ctx context.Context, baseURL, token string, s *session, method string, params any, out any) error {
	payload := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		payload["params"] = params
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("协议错误：无法编码请求")
	}

	resp, raw, err := c.post(ctx, baseURL, token, s, body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return errors.New("令牌被拒绝")
		}
		return fmt.Errorf("服务器返回 HTTP %d", resp.StatusCode)
	}
	if len(raw) == 0 {
		return errors.New("协议错误：响应为空")
	}

	var envelope rpcResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return errors.New("协议错误：响应不是合法的 JSON")
	}
	if envelope.Error != nil {
		message := strings.TrimSpace(envelope.Error.Message)
		if message == "" {
			message = "未知错误"
		}
		return fmt.Errorf("协议错误：%s", message)
	}
	if len(envelope.Result) == 0 {
		return errors.New("协议错误：响应缺少 result")
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return errors.New("协议错误：result 结构不符合预期")
	}
	return nil
}

func (c *Client) notify(ctx context.Context, baseURL, token string, s *session, method string) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	if err != nil {
		return errors.New("协议错误：无法编码通知")
	}
	resp, _, err := c.post(ctx, baseURL, token, s, body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("服务器返回 HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) post(ctx context.Context, baseURL, token string, s *session, body []byte) (*http.Response, []byte, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/mcp"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, nil, errors.New("协议错误：服务器地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	if s != nil {
		req.Header.Set("MCP-Protocol-Version", s.version)
		if s.sessionID != "" {
			req.Header.Set("Mcp-Session-Id", s.sessionID)
		}
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, nil, transportError(err)
	}
	defer resp.Body.Close()

	if s != nil && s.sessionID == "" {
		s.sessionID = resp.Header.Get("Mcp-Session-Id")
	}

	contentType := resp.Header.Get("Content-Type")

	limit := c.MaxResponseBytes
	if limit <= 0 {
		limit = 256 * 1024
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, nil, transportError(err)
	}
	if int64(len(raw)) > limit {
		return nil, nil, fmt.Errorf("协议错误：响应超过 %d 字节上限", limit)
	}
	// 202 notifications carry no body and no Content-Type; only a non-empty
	// success body is required to be JSON.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && len(raw) > 0 &&
		!strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		return resp, nil, fmt.Errorf("协议错误：响应不是 application/json（Content-Type: %s）", contentType)
	}
	return resp, raw, nil
}

// transportError turns dial and timeout failures into the two messages the
// teacher page shows. It deliberately never includes the URL or headers.
func transportError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("无法连接到服务器：连接超时")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return errors.New("无法连接到服务器：连接超时")
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) || errors.As(err, &netErr) {
		return errors.New("无法连接到服务器")
	}
	return errors.New("无法连接到服务器")
}

// redact removes the token from any message before it reaches a response or a
// log line. A remote server may echo the Authorization header in an error.
func redact(text, token string) string {
	if token == "" || text == "" {
		return text
	}
	return strings.ReplaceAll(text, token, "***")
}

func redactError(err error, token string) error {
	if err == nil {
		return nil
	}
	return errors.New(redact(err.Error(), token))
}
