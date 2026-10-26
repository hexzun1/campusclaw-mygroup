package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testToken = "test-token-abcdef"

// newFakeServer mimics the handshake and records the headers it saw.
func newFakeServer(t *testing.T, tools []Tool) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+" v="+r.Header.Get("MCP-Protocol-Version")+" s="+r.Header.Get("Mcp-Session-Id"))

		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			http.Error(w, "bad token "+testToken, http.StatusUnauthorized)
			return
		}
		if accept := r.Header.Get("Accept"); !strings.Contains(accept, "application/json") || !strings.Contains(accept, "text/event-stream") {
			http.Error(w, "bad accept", http.StatusBadRequest)
			return
		}

		var req struct {
			Method string          `json:"method"`
			ID     json.RawMessage `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "sess-1")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26"}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1, "result": map[string]any{"tools": tools},
			})
		default:
			http.Error(w, "unknown", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestDiscoverPerformsHandshakeAndSendsProtocolHeaders(t *testing.T) {
	tools := []Tool{
		{Name: "formula_hint", Description: "公式提示", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "unit_convert", Description: "单位换算"},
	}
	srv, seen := newFakeServer(t, tools)
	client := NewClient(2*time.Second, 2*time.Second, 256*1024)

	got, err := client.Discover(context.Background(), srv.URL, testToken)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 2 || got[0].Name != "formula_hint" || got[1].Name != "unit_convert" {
		t.Fatalf("tools = %+v", got)
	}
	if string(got[0].InputSchema) != `{"type":"object"}` {
		t.Fatalf("schema = %s", got[0].InputSchema)
	}
	if string(got[1].InputSchema) != `{}` {
		t.Fatalf("missing schema should default to {}, got %s", got[1].InputSchema)
	}

	if len(*seen) != 3 {
		t.Fatalf("requests = %v, want initialize, notification, tools/list", *seen)
	}
	// The initialize request has no session yet; the two follow-ups must carry
	// the negotiated version and the session id returned by initialize.
	for _, request := range (*seen)[1:] {
		if !strings.Contains(request, "s=sess-1") {
			t.Errorf("request %q did not carry the session id", request)
		}
		if !strings.Contains(request, "v=2025-03-26") {
			t.Errorf("request %q did not carry the negotiated protocol version", request)
		}
	}
}

func TestCallPerformsHandshakeAndKeepsOnlyText(t *testing.T) {
	var toolCallArguments json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			Params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			toolCallArguments = req.Params.Arguments
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"1500 m"},{"type":"image","data":"ignored"}]}}`))
		}
	}))
	defer srv.Close()

	client := NewClient(2*time.Second, 2*time.Second, 256*1024)
	result, err := client.Call(context.Background(), srv.URL, testToken, "unit_convert", json.RawMessage(`{"value":1.5}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.Text != "1500 m" || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if string(toolCallArguments) != `{"value":1.5}` {
		t.Fatalf("arguments sent = %s", toolCallArguments)
	}
}

func TestCallPropagatesIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"不支持跨量纲换算"}],"isError":true}}`))
		}
	}))
	defer srv.Close()

	client := NewClient(2*time.Second, 2*time.Second, 256*1024)
	result, err := client.Call(context.Background(), srv.URL, testToken, "unit_convert", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Text, "跨量纲") {
		t.Fatalf("result = %+v", result)
	}
}

func TestDiscoverErrorClassificationAndRedaction(t *testing.T) {
	cases := []struct {
		name      string
		handler   http.HandlerFunc
		baseURL   string
		timeout   time.Duration
		maxBytes  int64
		wantInMsg string
	}{
		{
			name: "令牌被拒绝",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "invalid token "+testToken, http.StatusUnauthorized)
			},
			timeout:   2 * time.Second,
			maxBytes:  256 * 1024,
			wantInMsg: "令牌被拒绝",
		},
		{
			name: "非 JSON 响应",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write([]byte("token " + testToken))
			},
			timeout:   2 * time.Second,
			maxBytes:  256 * 1024,
			wantInMsg: "不是 application/json",
		},
		{
			name: "SSE 响应",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {}\n\n"))
			},
			timeout:   2 * time.Second,
			maxBytes:  256 * 1024,
			wantInMsg: "不是 application/json",
		},
		{
			name: "协议错误回显令牌",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"bad ` + testToken + `"}}`))
			},
			timeout:   2 * time.Second,
			maxBytes:  256 * 1024,
			wantInMsg: "***",
		},
		{
			name: "响应过大",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`))
			},
			timeout:   2 * time.Second,
			maxBytes:  16,
			wantInMsg: "超过",
		},
		{
			name: "超时",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(300 * time.Millisecond)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
			},
			timeout:   50 * time.Millisecond,
			maxBytes:  256 * 1024,
			wantInMsg: "无法连接到服务器",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()

			client := NewClient(tc.timeout, tc.timeout, tc.maxBytes)
			_, err := client.Discover(context.Background(), srv.URL, testToken)
			if err == nil {
				t.Fatal("Discover succeeded, want error")
			}
			if !strings.Contains(err.Error(), tc.wantInMsg) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.wantInMsg)
			}
			if strings.Contains(err.Error(), testToken) {
				t.Fatalf("error leaks the token: %q", err)
			}
		})
	}
}

func TestDiscoverUnreachableServer(t *testing.T) {
	client := NewClient(200*time.Millisecond, 200*time.Millisecond, 256*1024)
	_, err := client.Discover(context.Background(), "http://127.0.0.1:1", testToken)
	if err == nil {
		t.Fatal("Discover succeeded against an unreachable server")
	}
	if !strings.Contains(err.Error(), "无法连接到服务器") {
		t.Fatalf("error = %q, want 无法连接到服务器", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("error leaks the token: %q", err)
	}
}

func TestRedact(t *testing.T) {
	if got := redact(fmt.Sprintf("echo %s twice %s", testToken, testToken), testToken); strings.Contains(got, testToken) {
		t.Fatalf("redact left the token in %q", got)
	}
	if got := redact("plain message", testToken); got != "plain message" {
		t.Fatalf("redact changed an unrelated message: %q", got)
	}
}
