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

	"campusclaw/backend/internal/db"
)

// TestFilterTools covers the connect-time cache policy (spec: 登记、连接与发现):
// illegal names are dropped, oversized inputSchema is dropped, and long
// descriptions are truncated to maxDescriptionRunes.
func TestFilterTools(t *testing.T) {
	longSchema := json.RawMessage(`{"type":"object","properties":{` +
		strings.Repeat(`"x":{"type":"string"},`, 400) + `"y":"z"}}`)
	if len(longSchema) <= maxSchemaBytes {
		t.Fatalf("fixture schema = %d bytes, want it to exceed %d", len(longSchema), maxSchemaBytes)
	}
	// 8KB exactly is kept; the check is strictly greater than maxSchemaBytes.
	exactSchema := json.RawMessage(`{"pad":"` + strings.Repeat("a", maxSchemaBytes-len(`{"pad":""}`)) + `"}`)
	if len(exactSchema) != maxSchemaBytes {
		t.Fatalf("fixture schema = %d bytes, want exactly %d", len(exactSchema), maxSchemaBytes)
	}

	got := filterTools(1, []Tool{
		{Name: "formula_hint", Description: "公式提示", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "Bad_Name", Description: "大写非法", InputSchema: json.RawMessage(`{}`)},
		{Name: "1leading", Description: "数字开头非法", InputSchema: json.RawMessage(`{}`)},
		{Name: strings.Repeat("n", 65), Description: "65 字超长非法", InputSchema: json.RawMessage(`{}`)},
		{Name: "huge_schema", Description: "9KB 结构应丢弃", InputSchema: longSchema},
		{Name: "exact_schema", Description: "恰好 8KB 应保留", InputSchema: exactSchema},
		{Name: "long_desc", Description: strings.Repeat("说", maxDescriptionRunes+50), InputSchema: json.RawMessage(`{}`)},
	})

	var names []string
	for _, tool := range got {
		names = append(names, tool.Name)
	}
	t.Logf("kept tools = %v", names)

	byName := map[string]db.MCPTool{}
	for _, tool := range got {
		byName[tool.Name] = tool
	}

	if _, ok := byName["formula_hint"]; !ok {
		t.Errorf("legal formula_hint was dropped, kept=%v", names)
	}
	for _, badName := range []string{"Bad_Name", "1leading", strings.Repeat("n", 65), "huge_schema"} {
		if _, ok := byName[badName]; ok {
			t.Errorf("tool %q should have been filtered out", badName)
		}
	}
	if _, ok := byName["exact_schema"]; !ok {
		t.Errorf("inputSchema of exactly %d bytes must be kept", maxSchemaBytes)
	}
	kept, ok := byName["long_desc"]
	if !ok {
		t.Fatalf("long_desc missing from %v", names)
	}
	if n := len([]rune(kept.Description)); n != maxDescriptionRunes {
		t.Errorf("long_desc description = %d runes, want truncated to %d", n, maxDescriptionRunes)
	}
	// 顺序保持：过滤不得重排远端返回的工具。
	if want := []string{"formula_hint", "exact_schema", "long_desc"}; len(names) != len(want) {
		t.Errorf("kept %d tools, want %d", len(names), len(want))
	} else {
		for i := range want {
			if names[i] != want[i] {
				t.Errorf("names = %v, want %v", names, want)
				break
			}
		}
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("abc", 10); got != "abc" {
		t.Errorf("short string changed: %q", got)
	}
	if got := truncateRunes("一二三四五", 3); got != "一二三" {
		t.Errorf("truncateRunes = %q, want 一二三 (runes, not bytes)", got)
	}
}

// TestDiscoverThenFilterAgainstFakeServer wires the client to an httptest
// server that exposes illegal / oversized / over-long tools, runs the same
// Discover → filterTools path as POST /connect, and asserts only the clean
// tools would reach the cache (task 3.3 integration verification).
func TestDiscoverThenFilterAgainstFakeServer(t *testing.T) {
	longSchema := json.RawMessage(`{"type":"object","properties":{` +
		strings.Repeat(`"p":{"type":"string"},`, 400) + `"q":1}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1,
				"result": map[string]any{"tools": []Tool{
					{Name: "good_tool", Description: "正常", InputSchema: json.RawMessage(`{"type":"object"}`)},
					{Name: "Bad tool", Description: "含空格与大写", InputSchema: json.RawMessage(`{}`)},
					{Name: "big_schema", Description: "超 8KB", InputSchema: longSchema},
					{Name: "long_desc", Description: strings.Repeat("x", maxDescriptionRunes+1), InputSchema: json.RawMessage(`{}`)},
				}},
			})
		default:
			http.Error(w, "unknown", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := newTestClient(2*time.Second, 2*time.Second, 256*1024)
	discovered, err := client.Discover(context.Background(), srv.URL, testToken)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(discovered) != 4 {
		t.Fatalf("discovered = %d tools, want the raw 4 before filtering", len(discovered))
	}

	cached := filterTools(7, discovered)
	if len(cached) != 2 {
		t.Fatalf("cached = %+v, want good_tool and long_desc only", cached)
	}
	if cached[0].Name != "good_tool" {
		t.Errorf("cached[0] = %q, want good_tool", cached[0].Name)
	}
	if n := len([]rune(cached[1].Description)); n != maxDescriptionRunes {
		t.Errorf("long_desc cached description = %d runes, want %d", n, maxDescriptionRunes)
	}
	// The audit/cache path must never see the token in tool data either.
	for _, tool := range cached {
		if strings.Contains(tool.Name+tool.Description+string(tool.InputSchema), testToken) {
			t.Errorf("cached tool leaks token: %+v", tool)
		}
	}
}

func TestValidateBaseURL(t *testing.T) {
	cases := []struct {
		raw     string
		wantErr bool
	}{
		{"http://mcp-demo:8090", false},
		{"https://example.com/mcp/api", false},
		{"http://example.com", false},
		{"ftp://example.com", true},
		{"http://u:p@example.com", true},
		{"http://example.com/mcp", true},
		{"http://example.com/api/mcp", true},
		{"not a url", true},
		{"", true},
		{fmt.Sprintf("http://%s", strings.Repeat("a", 195)), true},
	}
	for _, tc := range cases {
		err := validateBaseURL(tc.raw)
		if (err != nil) != tc.wantErr {
			t.Errorf("validateBaseURL(%q) err = %v, wantErr = %v", tc.raw, err, tc.wantErr)
		}
	}
}
