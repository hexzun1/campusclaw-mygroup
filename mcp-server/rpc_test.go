package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "test-token-123"

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newMux(&server{token: testToken}))
	t.Cleanup(srv.Close)
	return srv
}

func postMCP(t *testing.T, url, body string, authorized bool) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authorized {
		req.Header.Set("Authorization", "Bearer "+testToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp, payload
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

func decodeEnvelope(t *testing.T, payload []byte) rpcEnvelope {
	t.Helper()
	var env rpcEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("decode envelope %q: %v", payload, err)
	}
	return env
}

func TestRPCErrorCodes(t *testing.T) {
	srv := newTestServer(t)

	cases := []struct {
		name     string
		body     string
		wantCode int
	}{
		{"非法 JSON", `not json`, codeParseError},
		{"缺 method", `{"jsonrpc":"2.0","id":1}`, codeInvalidRequest},
		{"未知方法", `{"jsonrpc":"2.0","id":2,"method":"tools/unknown"}`, codeMethodNotFound},
		{"未知工具", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope","arguments":{}}}`, codeInvalidParams},
		{"缺少 topic", `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"formula_hint","arguments":{}}}`, codeInvalidParams},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, payload := postMCP(t, srv.URL, tc.body, true)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (JSON-RPC errors ride on 200)", resp.StatusCode)
			}
			env := decodeEnvelope(t, payload)
			if env.Error == nil {
				t.Fatalf("payload %s has no error", payload)
			}
			if env.Error.Code != tc.wantCode {
				t.Fatalf("error code = %d, want %d (%s)", env.Error.Code, tc.wantCode, env.Error.Message)
			}
			if strings.TrimSpace(env.Error.Message) == "" {
				t.Fatal("error message is empty")
			}
		})
	}
}

func TestRPCRejectsOversizedBody(t *testing.T) {
	srv := newTestServer(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"padding":"` +
		strings.Repeat("x", 65<<10) + `"}}`

	resp, _ := postMCP(t, srv.URL, body, true)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
}

func TestRPCInitialize(t *testing.T) {
	srv := newTestServer(t)
	resp, payload := postMCP(t, srv.URL,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools map[string]any `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	env := decodeEnvelope(t, payload)
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if result.ProtocolVersion != "2024-11-05" {
		t.Fatalf("protocolVersion = %q, want the requested 2024-11-05", result.ProtocolVersion)
	}
	if result.Capabilities.Tools == nil {
		t.Fatal("capabilities.tools is missing")
	}
	if result.ServerInfo.Name != serverName || result.ServerInfo.Version == "" {
		t.Fatalf("serverInfo = %+v, want name %q and a version", result.ServerInfo, serverName)
	}
}

func TestRPCInitializeFallsBackToLatestVersion(t *testing.T) {
	srv := newTestServer(t)
	_, payload := postMCP(t, srv.URL,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`, true)

	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	env := decodeEnvelope(t, payload)
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if result.ProtocolVersion != latestProtocolVersion {
		t.Fatalf("protocolVersion = %q, want %q", result.ProtocolVersion, latestProtocolVersion)
	}
}

func TestRPCNotificationReturns202WithoutBody(t *testing.T) {
	srv := newTestServer(t)
	resp, payload := postMCP(t, srv.URL, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, true)

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if len(payload) != 0 {
		t.Fatalf("body = %q, want empty", payload)
	}
}

func TestRPCToolsListExposesBothTools(t *testing.T) {
	srv := newTestServer(t)
	_, payload := postMCP(t, srv.URL, `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`, true)

	var result struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	env := decodeEnvelope(t, payload)
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	if len(result.Tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(result.Tools))
	}
	names := map[string]bool{}
	for _, tool := range result.Tools {
		names[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		if tool.InputSchema["type"] != "object" {
			t.Errorf("%s inputSchema.type = %v, want object", tool.Name, tool.InputSchema["type"])
		}
	}
	if !names["formula_hint"] || !names["unit_convert"] {
		t.Fatalf("tool names = %v, want formula_hint and unit_convert", names)
	}
}

func TestRPCAuthentication(t *testing.T) {
	srv := newTestServer(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	if resp, _ := postMCP(t, srv.URL, body, false); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without token: status = %d, want 401", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", resp.StatusCode)
	}

	if resp, _ := postMCP(t, srv.URL, body, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("correct token: status = %d, want 200", resp.StatusCode)
	}
}

func TestRPCHealthNeedsNoToken(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if string(payload) != `{"status":"ok"}` {
		t.Fatalf("body = %q, want {\"status\":\"ok\"}", payload)
	}
}

func TestRPCToolsCallReturnsText(t *testing.T) {
	srv := newTestServer(t)
	_, payload := postMCP(t, srv.URL,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"formula_hint","arguments":{"topic":"勾股定理"}}}`, true)

	env := decodeEnvelope(t, payload)
	var result struct {
		Content []contentItem `json:"content"`
		IsError bool          `json:"isError"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("decode tools/call: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" || result.Content[0].Text == "" {
		t.Fatalf("content = %+v, want one non-empty text item", result.Content)
	}
	if result.IsError {
		t.Fatal("isError = true, want false")
	}
}
