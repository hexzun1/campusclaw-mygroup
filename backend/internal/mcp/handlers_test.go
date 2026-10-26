package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
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

// TestTokenBytes is spec 「工具服务器登记与管理」Scenario 令牌按字节计长度: the
// bound counts UTF-8 bytes, so 171 Chinese characters (513 bytes) fail and 170
// (510 bytes) pass.
func TestTokenBytes(t *testing.T) {
	cases := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{"空", "", true},
		{"1 字节", "a", false},
		{"512 字节", strings.Repeat("a", 512), false},
		{"513 字节", strings.Repeat("a", 513), true},
		{"170 个汉字=510 字节", strings.Repeat("令", 170), false},
		{"171 个汉字=513 字节", strings.Repeat("令", 171), true},
		{"256 个汉字=768 字节", strings.Repeat("令", 256), true},
	}
	for _, tc := range cases {
		err := validateToken(tc.token)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: validateToken(%d bytes) err = %v, wantErr = %v",
				tc.name, len(tc.token), err, tc.wantErr)
		}
		if err != nil && !strings.Contains(err.Error(), "字节") {
			t.Errorf("%s: error = %q, want it to say 字节", tc.name, err)
		}
	}
}

// TestDuplicateTools is spec 「连接与工具发现」Scenario 重名工具去重 at the
// filter: the first occurrence wins and keeps its description.
func TestDuplicateTools(t *testing.T) {
	got := filterTools(9, []Tool{
		{Name: "echo", Description: "第一条说明", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "echo", Description: "第二条说明", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "second_tool", Description: "另一个工具", InputSchema: json.RawMessage(`{}`)},
	})

	if len(got) != 2 {
		t.Fatalf("kept = %+v, want echo and second_tool", got)
	}
	if got[0].Name != "echo" || got[0].Description != "第一条说明" {
		t.Errorf("echo = %+v, want the first occurrence", got[0])
	}
	if got[1].Name != "second_tool" {
		t.Errorf("order = %+v, want the first occurrence order kept", got)
	}
}

// fakeConnectStore is the in-memory connectStore used to make the cache write
// fail without a database.
type fakeConnectStore struct {
	server       *db.MCPServer
	token        string
	replaceErr   error
	markErr      error
	markedWith   string
	replacedWith []db.MCPTool
}

func (f *fakeConnectStore) Server(ctx context.Context, classID, id int) (*db.MCPServer, error) {
	if f.server == nil {
		return nil, db.ErrNotFound
	}
	return f.server, nil
}

func (f *fakeConnectStore) Token(ctx context.Context, classID, id int) (string, error) {
	if f.server == nil {
		return "", db.ErrNotFound
	}
	return f.token, nil
}

func (f *fakeConnectStore) ReplaceTools(ctx context.Context, classID, id int, tools []db.MCPTool) error {
	f.replacedWith = tools
	return f.replaceErr
}

func (f *fakeConnectStore) MarkError(ctx context.Context, classID, id int, message string) error {
	f.markedWith = message
	return f.markErr
}

// TestConnectCacheWriteFailureMarksError is spec 「连接与工具发现」: a discovery
// that succeeds but cannot be cached answers 503 and leaves the server in
// error with a readable last_error (design D8).
func TestConnectCacheWriteFailureMarksError(t *testing.T) {
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
		case "tools/list":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"formula_hint","description":"公式","inputSchema":{}}]}}`))
		}
	}))
	defer srv.Close()

	store := &fakeConnectStore{
		server:     &db.MCPServer{ID: 11, Name: "测试", BaseURL: srv.URL, Status: db.MCPServerDisconnected},
		token:      testToken,
		replaceErr: errors.New("insert mcp tool echo: Error 1062"),
	}
	h := &Handlers{Client: newTestClient(2*time.Second, 2*time.Second, 256*1024), store: store}

	r := httptest.NewRequest(http.MethodPost, "/api/mcp/servers/11/connect", nil)
	r.SetPathValue("id", "11")
	r = r.WithContext(httpapi.WithSessionUser(r.Context(), &db.SessionUser{UserID: 1, ClassID: 1, Role: db.RoleTeacher}))
	w := httptest.NewRecorder()

	h.Connect(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
	if store.markedWith != "工具缓存写入失败，请重试连接" {
		t.Errorf("last_error = %q, want 工具缓存写入失败，请重试连接", store.markedWith)
	}
	if len(store.replacedWith) != 1 || store.replacedWith[0].Name != "formula_hint" {
		t.Errorf("replaced tools = %+v, want the filtered list", store.replacedWith)
	}
	if strings.Contains(w.Body.String(), testToken) {
		t.Errorf("the response leaks the token: %s", w.Body.String())
	}
}
