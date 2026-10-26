package gateway

import (
	"bufio"
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

// ChatConfig configures the chat gateway client.
type ChatConfig struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

// ChatMessage is one OpenAI-compatible message. ToolCalls is set on the
// assistant message that requested calls and ToolCallID on the matching tool
// result; neither field is ever populated from client input.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// Roles accepted on the wire. The server builds its own system message and
// drops any client-supplied one (spec: 带引用的问答).
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// ToolDefinition declares one tool as an OpenAI-compatible function. Name is
// the host's `tool__server` mapping, never the raw `tool@server` reference
// (design.md Decision 3).
type ToolDefinition struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is the declaration part of a ToolDefinition.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolCall is one function call the model asked for. Arguments stays a raw
// string so the host can reject malformed JSON instead of trusting it.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type,omitempty"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction names the requested function and carries its raw arguments.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// AssistantMessage is the non-streamed reply of the tool stage.
type AssistantMessage struct {
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ChatClient asks the chat gateway for an answer, streamed or complete.
type ChatClient struct {
	client *Client
	// stream is the same gateway client with streaming-appropriate timeouts
	// (design.md Decision 8): no total timeout, because a streamed answer may
	// legitimately outlive GATEWAY_TIMEOUT_SECONDS; the header timeout still
	// bounds a gateway that never answers, and cancelling the request context
	// abandons the stream.
	stream *Client
}

// NewChatClient builds the client.
func NewChatClient(cfg ChatConfig) *ChatClient {
	client := NewClient(cfg.BaseURL, cfg.APIKey, cfg.Model, cfg.Timeout)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = client.http.Timeout
	stream := &Client{
		baseURL: client.baseURL,
		apiKey:  client.apiKey,
		model:   client.model,
		http:    &http.Client{Transport: transport},
	}

	return &ChatClient{client: client, stream: stream}
}

// Model is the configured model name.
func (c *ChatClient) Model() string { return c.client.Model() }

// Chat sends the messages and returns the assistant's reply.
//
// The request is never streamed: /api/ask answers with one complete JSON body
// (proposal: Non-goals), so stream is explicitly false and the whole response
// is decoded in one go. A missing choice or an unusable body is reported as
// ErrUnavailable, like every other gateway failure.
func (c *ChatClient) Chat(ctx context.Context, messages []ChatMessage) (string, error) {
	if len(messages) == 0 {
		return "", upstream.Unavailable("the chat request has no messages", nil)
	}

	var resp chatResponse
	payload := map[string]any{
		"model":    c.client.Model(),
		"messages": messages,
		"stream":   false,
	}
	if err := c.client.post(ctx, "/chat/completions", payload, &resp); err != nil {
		return "", err
	}

	if len(resp.Choices) == 0 {
		return "", upstream.Unavailable("the chat gateway returned no choices", nil)
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

// ChatWithTools runs one non-streamed tool-stage exchange: the messages plus
// function declarations, tool_choice auto. The reply may carry tool_calls
// instead of (or in addition to) text; arguments are returned verbatim for the
// host to validate (design.md Decision 8).
func (c *ChatClient) ChatWithTools(ctx context.Context, messages []ChatMessage, tools []ToolDefinition) (AssistantMessage, error) {
	if len(messages) == 0 {
		return AssistantMessage{}, upstream.Unavailable("the chat request has no messages", nil)
	}
	if len(tools) == 0 {
		return AssistantMessage{}, upstream.Unavailable("the tool request has no tools", nil)
	}

	var resp chatResponse
	payload := map[string]any{
		"model":       c.client.Model(),
		"messages":    messages,
		"stream":      false,
		"tools":       tools,
		"tool_choice": "auto",
	}
	if err := c.client.post(ctx, "/chat/completions", payload, &resp); err != nil {
		return AssistantMessage{}, err
	}
	if len(resp.Choices) == 0 {
		return AssistantMessage{}, upstream.Unavailable("the chat gateway returned no choices", nil)
	}
	message := resp.Choices[0].Message
	return AssistantMessage{
		Content:   strings.TrimSpace(message.Content),
		ToolCalls: message.ToolCalls,
	}, nil
}

// StreamChat asks for a streamed answer (stream:true) and calls onDelta for
// every non-empty content piece, in upstream order.
//
// A failure before the first delta and a failure after some deltas are both
// returned as ErrUnavailable; the caller distinguishes them by whether onDelta
// has already yielded text (design.md Decision 5): a pre-first-text failure
// answers with a plain 503, a later one is reported inside the event stream.
// An error from onDelta aborts the stream and is returned as-is, wrapped in
// ErrUnavailable. The stream must end with the upstream's [DONE] marker; an
// early close is a failure.
func (c *ChatClient) StreamChat(ctx context.Context, messages []ChatMessage, onDelta func(string) error) error {
	if len(messages) == 0 {
		return upstream.Unavailable("the chat request has no messages", nil)
	}

	body, err := json.Marshal(map[string]any{
		"model":    c.client.Model(),
		"messages": messages,
		"stream":   true,
	})
	if err != nil {
		return upstream.Unavailable("could not encode the gateway request", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.stream.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return upstream.Unavailable("could not build the gateway request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.stream.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.stream.apiKey)
	}

	resp, err := c.stream.http.Do(req)
	if err != nil {
		return upstream.Unavailable("the gateway is unreachable", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodyBytes))
		return upstream.Unavailable(fmt.Sprintf("the gateway answered HTTP %d", resp.StatusCode), nil)
	}

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")

		if data, ok := strings.CutPrefix(line, "data:"); ok {
			data = strings.TrimSpace(data)
			if data == "[DONE]" {
				return nil
			}
			if delta := deltaContent(data); delta != "" {
				if err := onDelta(delta); err != nil {
					return upstream.Unavailable("the streamed answer could not be delivered", err)
				}
			}
		}

		if err != nil {
			if err == io.EOF {
				return upstream.Unavailable("the gateway closed the stream before its end marker", nil)
			}
			return upstream.Unavailable("the gateway stream was interrupted", err)
		}
	}
}

// deltaContent extracts choices[0].delta.content of one SSE data line. A line
// that carries no content — a role-only chunk, an unparsable frame — yields "".
func deltaContent(data string) string {
	var chunk struct {
		Choices []struct {
			Delta ChatMessage `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(data), &chunk); err != nil || len(chunk.Choices) == 0 {
		return ""
	}
	return chunk.Choices[0].Delta.Content
}

type chatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
}
