package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"campusclaw/backend/internal/upstream"
)

type stubStats struct {
	EmbeddingCalls  int             `json:"embedding_calls"`
	ChatCalls       int             `json:"chat_calls"`
	LastChatRequest json.RawMessage `json:"last_chat_request"`
}

func readStubStats(t *testing.T, baseURL string) stubStats {
	t.Helper()
	resp, err := http.Get(baseURL + "/stats")
	if err != nil {
		t.Fatalf("GET /stats: %v", err)
	}
	defer resp.Body.Close()

	var stats stubStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("decode /stats: %v", err)
	}
	return stats
}

func newStubChatClient(t *testing.T, baseURL string) *ChatClient {
	t.Helper()
	return NewChatClient(ChatConfig{
		BaseURL: baseURL, APIKey: "stub-chat-key", Model: "stub-chat", Timeout: 10 * time.Second,
	})
}

// TestStubGatewayChatCountsAndReturnsText is the verify from tasks.md 4.3: one
// call increments the stub's chat counter by exactly one and returns text.
func TestStubGatewayChatCountsAndReturnsText(t *testing.T) {
	baseURL := stubGatewayURL(t)
	client := newStubChatClient(t, baseURL)

	before := readStubStats(t, baseURL).ChatCalls

	reply, err := client.Chat(context.Background(), []ChatMessage{
		{Role: RoleSystem, Content: "只能依据下列资料回答。"},
		{Role: RoleUser, Content: "资料 [1] 甲\n问题：这是什么？"},
	})
	if err != nil {
		t.Fatalf("Chat against the stub failed: %v", err)
	}
	if strings.TrimSpace(reply) == "" {
		t.Fatal("the stub returned empty text")
	}
	if !strings.Contains(reply, "[1]") {
		t.Fatalf("reply %q does not echo the material number it received", reply)
	}

	after := readStubStats(t, baseURL)
	if after.ChatCalls != before+1 {
		t.Fatalf("chat calls went from %d to %d, want exactly one more", before, after.ChatCalls)
	}
	if !strings.Contains(string(after.LastChatRequest), "资料 [1]") {
		t.Fatalf("the stub did not record the request body: %s", after.LastChatRequest)
	}
}

func TestStubGatewayChatFailureIsUnavailable(t *testing.T) {
	baseURL := stubGatewayURL(t)
	client := newStubChatClient(t, baseURL)

	setStubControl(t, baseURL, map[string]bool{"chat_fail": true})
	t.Cleanup(func() { setStubControl(t, baseURL, map[string]bool{"chat_fail": false}) })

	_, err := client.Chat(context.Background(), []ChatMessage{{Role: RoleUser, Content: "问题"}})
	if err == nil {
		t.Fatal("Chat succeeded while the stub was failing")
	}
	if !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error %v is not recognised by errors.Is(err, upstream.ErrUnavailable)", err)
	}
	for _, forbidden := range []string{"stubgateway", "8090", "stub-chat-key"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error %q leaks %q", err.Error(), forbidden)
		}
	}
}

func TestStubGatewayChatHonoursStreamFalse(t *testing.T) {
	baseURL := stubGatewayURL(t)
	client := newStubChatClient(t, baseURL)

	if _, err := client.Chat(context.Background(), []ChatMessage{{Role: RoleUser, Content: "资料 [2] 乙"}}); err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	raw := string(readStubStats(t, baseURL).LastChatRequest)
	if !strings.Contains(raw, `"stream":false`) {
		t.Fatalf("the request did not pin stream to false: %s", raw)
	}
}
