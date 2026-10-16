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

// TestStubGatewayStreamChatDeliversPieces is the verify from tasks.md 4.6: a
// streamed call against the stub yields at least two delta pieces whose
// concatenation is the same text the non-streamed call returns, and the stub
// recorded stream:true.
func TestStubGatewayStreamChatDeliversPieces(t *testing.T) {
	baseURL := stubGatewayURL(t)
	client := newStubChatClient(t, baseURL)

	messages := []ChatMessage{{Role: RoleUser, Content: "资料 [1] 甲\n资料 [2] 乙"}}

	want, err := client.Chat(context.Background(), messages)
	if err != nil {
		t.Fatalf("Chat against the stub failed: %v", err)
	}

	var pieces []string
	err = client.StreamChat(context.Background(), messages, func(piece string) error {
		pieces = append(pieces, piece)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChat against the stub failed: %v", err)
	}

	if len(pieces) < 2 {
		t.Fatalf("got %d stream pieces (%q), want at least 2", len(pieces), pieces)
	}
	if got := strings.Join(pieces, ""); got != want {
		t.Fatalf("concatenated pieces %q differ from the non-streamed answer %q", got, want)
	}

	raw := string(readStubStats(t, baseURL).LastChatRequest)
	if !strings.Contains(raw, `"stream":true`) {
		t.Fatalf("the streamed request did not set stream:true: %s", raw)
	}
}

// TestStubGatewayStreamChatFailureBeforeAndAfterFirstDelta is the verify from
// tasks.md 4.6: "首段前失败" and "中途中断" are distinguishable by whether any
// delta arrived before the error.
func TestStubGatewayStreamChatFailureBeforeAndAfterFirstDelta(t *testing.T) {
	baseURL := stubGatewayURL(t)
	client := newStubChatClient(t, baseURL)
	ctx := context.Background()
	messages := []ChatMessage{{Role: RoleUser, Content: "资料 [1] 甲"}}

	setStubControl(t, baseURL, map[string]bool{"fail_before_first_delta": true})
	t.Cleanup(func() { setStubControl(t, baseURL, map[string]bool{"fail_before_first_delta": false}) })

	var pieces []string
	err := client.StreamChat(ctx, messages, func(piece string) error {
		pieces = append(pieces, piece)
		return nil
	})
	if err == nil {
		t.Fatal("StreamChat succeeded while the stub was failing before the first delta")
	}
	if !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error %v is not recognised by errors.Is(err, upstream.ErrUnavailable)", err)
	}
	if len(pieces) != 0 {
		t.Fatalf("a pre-stream failure delivered %d pieces: %q", len(pieces), pieces)
	}

	setStubControl(t, baseURL, map[string]bool{"fail_before_first_delta": false})
	setStubControlJSON(t, baseURL, map[string]any{"abort_after_deltas": 2})
	t.Cleanup(func() { setStubControlJSON(t, baseURL, map[string]any{"abort_after_deltas": -1}) })

	pieces = nil
	err = client.StreamChat(ctx, messages, func(piece string) error {
		pieces = append(pieces, piece)
		return nil
	})
	if err == nil {
		t.Fatal("StreamChat succeeded although the stub aborted mid-stream")
	}
	if !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error %v is not recognised by errors.Is(err, upstream.ErrUnavailable)", err)
	}
	if len(pieces) != 2 {
		t.Fatalf("got %d pieces before the abort (%q), want exactly 2", len(pieces), pieces)
	}

	for _, forbidden := range []string{"stubgateway", "8090", "stub-chat-key"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error %q leaks %q", err.Error(), forbidden)
		}
	}
}

// TestStubGatewayStreamChatControlText is the "指定输出文字" switch: the exact
// text wins over the material echo, streamed and non-streamed alike.
func TestStubGatewayStreamChatControlText(t *testing.T) {
	baseURL := stubGatewayURL(t)
	client := newStubChatClient(t, baseURL)

	const want = "答案见[1]和[2]。"
	setStubControlJSON(t, baseURL, map[string]any{"chat_text": want})
	t.Cleanup(func() { setStubControlJSON(t, baseURL, map[string]any{"chat_text": ""}) })

	reply, err := client.Chat(context.Background(), []ChatMessage{{Role: RoleUser, Content: "资料 [1] 甲"}})
	if err != nil {
		t.Fatalf("Chat against the stub failed: %v", err)
	}
	if reply != want {
		t.Fatalf("non-streamed reply = %q, want the control text %q", reply, want)
	}

	var pieces []string
	err = client.StreamChat(context.Background(), []ChatMessage{{Role: RoleUser, Content: "资料 [1] 甲"}}, func(piece string) error {
		pieces = append(pieces, piece)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChat against the stub failed: %v", err)
	}
	if got := strings.Join(pieces, ""); got != want {
		t.Fatalf("streamed pieces %q differ from the control text %q", got, want)
	}
}
