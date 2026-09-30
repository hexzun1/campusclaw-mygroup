package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"campusclaw/backend/internal/upstream"
)

func TestChatSendsMessagesAndParsesReply(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "  依据资料 [1] 作答。  "}},
			},
		})
	}))
	t.Cleanup(srv.Close)

	client := NewChatClient(ChatConfig{
		BaseURL: srv.URL, APIKey: testAPIKey, Model: "chat-model", Timeout: 5 * time.Second,
	})

	reply, err := client.Chat(context.Background(), []ChatMessage{
		{Role: RoleSystem, Content: "只能依据资料回答。"},
		{Role: RoleUser, Content: "问题是什么？"},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if reply != "依据资料 [1] 作答。" {
		t.Fatalf("reply = %q, want the trimmed assistant content", reply)
	}

	if gotBody["model"] != "chat-model" {
		t.Fatalf("model = %v, want chat-model", gotBody["model"])
	}
	if gotBody["stream"] != false {
		t.Fatalf("stream = %v, want false (the api never streams)", gotBody["stream"])
	}
	messages, ok := gotBody["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %v, want the two sent messages", gotBody["messages"])
	}
	if client.Model() != "chat-model" {
		t.Fatalf("Model() = %q, want chat-model", client.Model())
	}
}

func TestChatUsesBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		})
	}))
	t.Cleanup(srv.Close)

	client := NewChatClient(ChatConfig{BaseURL: srv.URL, APIKey: testAPIKey, Model: "m"})
	if _, err := client.Chat(context.Background(), []ChatMessage{{Role: RoleUser, Content: "hi"}}); err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if gotAuth != "Bearer "+testAPIKey {
		t.Fatalf("Authorization = %q, want a bearer token", gotAuth)
	}
}

func TestChatSanitizesUpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"upstream exploded at http://internal.example:1234"}`))
	}))
	t.Cleanup(srv.Close)

	client := NewChatClient(ChatConfig{BaseURL: srv.URL, APIKey: testAPIKey, Model: "m", Timeout: 5 * time.Second})
	_, err := client.Chat(context.Background(), []ChatMessage{{Role: RoleUser, Content: "hi"}})
	if !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error = %v, want upstream.ErrUnavailable", err)
	}
	for _, forbidden := range []string{"127.0.0.1", testAPIKey, "internal.example", "upstream exploded"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error %q leaks %q", err.Error(), forbidden)
		}
	}
}

func TestChatRejectsResponseWithoutChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewChatClient(ChatConfig{BaseURL: srv.URL, Model: "m"})
	if _, err := client.Chat(context.Background(), []ChatMessage{{Role: RoleUser, Content: "hi"}}); !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error = %v, want upstream.ErrUnavailable", err)
	}
}

func TestChatWithNoMessagesMakesNoRequest(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	t.Cleanup(srv.Close)

	client := NewChatClient(ChatConfig{BaseURL: srv.URL, Model: "m"})
	if _, err := client.Chat(context.Background(), nil); !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error = %v, want upstream.ErrUnavailable", err)
	}
	if called {
		t.Fatal("an empty message list still reached the gateway")
	}
}

func TestChatUnreachableGatewayIsSanitized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	client := NewChatClient(ChatConfig{BaseURL: srv.URL, APIKey: testAPIKey, Model: "m", Timeout: 2 * time.Second})
	srv.Close()

	_, err := client.Chat(context.Background(), []ChatMessage{{Role: RoleUser, Content: "hi"}})
	if !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error = %v, want upstream.ErrUnavailable", err)
	}
	for _, forbidden := range []string{"127.0.0.1", testAPIKey, "dial"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error %q leaks %q", err.Error(), forbidden)
		}
	}
}

func TestChatHonoursContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	client := NewChatClient(ChatConfig{BaseURL: srv.URL, Model: "m", Timeout: 5 * time.Second})
	if _, err := client.Chat(ctx, []ChatMessage{{Role: RoleUser, Content: "hi"}}); !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error = %v, want upstream.ErrUnavailable", err)
	}
}
