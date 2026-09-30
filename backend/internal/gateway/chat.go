package gateway

import (
	"context"
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

// ChatMessage is one OpenAI-compatible message.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Roles accepted on the wire. The server builds its own system message and
// drops any client-supplied one (spec: 带引用的问答).
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// ChatClient asks the chat gateway for a complete answer.
type ChatClient struct {
	client *Client
}

// NewChatClient builds the client.
func NewChatClient(cfg ChatConfig) *ChatClient {
	return &ChatClient{client: NewClient(cfg.BaseURL, cfg.APIKey, cfg.Model, cfg.Timeout)}
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

type chatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
}
