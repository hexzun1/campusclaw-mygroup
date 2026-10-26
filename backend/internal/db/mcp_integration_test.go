package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func createTestMCPServer(t *testing.T, conn *sql.DB, classID, createdBy int, name, token string) int {
	t.Helper()
	id, err := CreateServer(context.Background(), conn, classID, name, "http://127.0.0.1:1", token, createdBy, "")
	if err != nil {
		t.Fatalf("insert test mcp server: %v", err)
	}
	t.Cleanup(func() {
		if _, err := conn.ExecContext(context.Background(), `DELETE FROM mcp_servers WHERE id = ?`, id); err != nil {
			t.Errorf("cleanup mcp server %d: %v", id, err)
		}
	})
	return id
}

func sampleTool(name string) MCPTool {
	return MCPTool{
		Name:        name,
		Description: "测试工具 " + name,
		InputSchema: json.RawMessage(`{"type":"object","required":["topic"]}`),
	}
}

// TestMCPCrossClassReadsAreNotFound: an A-class id must be ErrNotFound for a
// B-class caller on every read and write path (spec: 逐班授权与令牌保护).
func TestMCPCrossClassReadsAreNotFound(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, classB, teacherA := fixture(t, conn)

	serverID := createTestMCPServer(t, conn, classA, teacherA, "测试服务器-甲班", "token-abc")

	if _, err := GetServer(ctx, conn, serverID, classB); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetServer(B as A) = %v, want ErrNotFound", err)
	}
	if _, err := GetServerByName(ctx, conn, classB, "测试服务器-甲班"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetServerByName(B) = %v, want ErrNotFound", err)
	}
	if _, err := GetServerToken(ctx, conn, serverID, classB); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetServerToken(B) = %v, want ErrNotFound", err)
	}
	if tools, err := ListServerTools(ctx, conn, serverID, classB); err != nil || len(tools) != 0 {
		t.Errorf("ListServerTools(B) = %v, %v; want empty list", tools, err)
	}
	if err := ReplaceServerTools(ctx, conn, serverID, classB, []MCPTool{sampleTool("formula_hint")}); !errors.Is(err, ErrNotFound) {
		t.Errorf("ReplaceServerTools(B) = %v, want ErrNotFound", err)
	}
	if err := MarkServerError(ctx, conn, serverID, classB, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkServerError(B) = %v, want ErrNotFound", err)
	}
	if err := DisconnectServer(ctx, conn, serverID, classB); !errors.Is(err, ErrNotFound) {
		t.Errorf("DisconnectServer(B) = %v, want ErrNotFound", err)
	}
	if err := DeleteServer(ctx, conn, serverID, classB); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteServer(B) = %v, want ErrNotFound", err)
	}

	// Positive control: the owning class still reads it.
	if server, err := GetServer(ctx, conn, serverID, classA); err != nil || server.Name != "测试服务器-甲班" {
		t.Errorf("GetServer(A) = %+v, %v", server, err)
	}
}

// TestMCPReplaceToolsRollback: a failure while inserting the new cache must
// leave the previous cache and status untouched.
func TestMCPReplaceToolsRollback(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, _, teacherA := fixture(t, conn)

	id := createTestMCPServer(t, conn, classA, teacherA, "测试服务器-回滚", "token-abc")
	if err := ReplaceServerTools(ctx, conn, id, classA, []MCPTool{sampleTool("formula_hint")}); err != nil {
		t.Fatalf("replace with first cache: %v", err)
	}

	invalid := MCPTool{Name: strings.Repeat("x", 65), Description: "过长", InputSchema: json.RawMessage(`{}`)}
	if err := ReplaceServerTools(ctx, conn, id, classA, []MCPTool{sampleTool("unit_convert"), invalid}); err == nil {
		t.Fatal("replace with an over-long tool name succeeded, want error")
	}

	tools, err := ListServerTools(ctx, conn, id, classA)
	if err != nil {
		t.Fatalf("list tools after failed replace: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "formula_hint" {
		t.Fatalf("cache after failed replace = %+v, want the original formula_hint", tools)
	}
	server, err := GetServer(ctx, conn, id, classA)
	if err != nil {
		t.Fatalf("get server after failed replace: %v", err)
	}
	if server.Status != MCPServerConnected || server.ToolCount != 1 {
		t.Fatalf("server after failed replace = %+v, want connected with one tool", server)
	}
}

// TestMCPListServersNeverReturnsTheToken: the list projection exposes only
// has_token, and tool_count follows the cache.
func TestMCPListServersNeverReturnsTheToken(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, _, teacherA := fixture(t, conn)

	const token = "very-secret-mcp-token"
	id := createTestMCPServer(t, conn, classA, teacherA, "测试服务器-令牌", token)

	servers, err := ListServers(ctx, conn, classA)
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	var found *MCPServer
	for i := range servers {
		if servers[i].ID == id {
			found = &servers[i]
		}
	}
	if found == nil {
		t.Fatal("created server missing from the list")
	}
	if !found.HasToken || found.Status != MCPServerDisconnected || found.ToolCount != 0 {
		t.Fatalf("server = %+v, want has_token, disconnected and zero tools", found)
	}
	payload, err := json.Marshal(servers)
	if err != nil {
		t.Fatalf("marshal servers: %v", err)
	}
	if strings.Contains(string(payload), token) {
		t.Fatalf("list payload leaks the token: %s", payload)
	}

	stored, err := GetServerToken(ctx, conn, id, classA)
	if err != nil {
		t.Fatalf("GetServerToken: %v", err)
	}
	if stored != token {
		t.Fatalf("GetServerToken = %q, want the stored token", stored)
	}

	if err := ReplaceServerTools(ctx, conn, id, classA, []MCPTool{sampleTool("formula_hint")}); err != nil {
		t.Fatalf("replace tools: %v", err)
	}
	servers, err = ListServers(ctx, conn, classA)
	if err != nil {
		t.Fatalf("ListServers after replace: %v", err)
	}
	for _, server := range servers {
		if server.ID == id && server.ToolCount != 1 {
			t.Fatalf("tool_count = %d, want 1", server.ToolCount)
		}
	}
}

func TestMCPDisconnectAndErrorClearTheCache(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, _, teacherA := fixture(t, conn)

	id := createTestMCPServer(t, conn, classA, teacherA, "测试服务器-断开", "token-abc")
	if err := ReplaceServerTools(ctx, conn, id, classA, []MCPTool{sampleTool("formula_hint")}); err != nil {
		t.Fatalf("replace tools: %v", err)
	}

	if err := MarkServerError(ctx, conn, id, classA, "令牌被拒绝"); err != nil {
		t.Fatalf("MarkServerError: %v", err)
	}
	server, err := GetServer(ctx, conn, id, classA)
	if err != nil {
		t.Fatalf("GetServer after error: %v", err)
	}
	if server.Status != MCPServerError || server.LastError != "令牌被拒绝" || server.ToolCount != 0 {
		t.Fatalf("server after error = %+v", server)
	}

	if err := DisconnectServer(ctx, conn, id, classA); err != nil {
		t.Fatalf("DisconnectServer: %v", err)
	}
	server, err = GetServer(ctx, conn, id, classA)
	if err != nil {
		t.Fatalf("GetServer after disconnect: %v", err)
	}
	if server.Status != MCPServerDisconnected || server.LastError != "" || server.ToolCount != 0 {
		t.Fatalf("server after disconnect = %+v", server)
	}
}

func TestMCPToolCallsAreClassScoped(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, classB, teacherA := fixture(t, conn)
	studentA1 := userIDByUsername(t, conn, "student_a1")

	agent := createTestAgent(t, conn, classA, "测试智能体-审计", nil, nil)
	conv := createTestConversation(t, conn, classA, studentA1, agent)
	t.Cleanup(func() {
		if _, err := conn.ExecContext(context.Background(),
			`DELETE FROM mcp_tool_calls WHERE conversation_id = ?`, conv); err != nil {
			t.Errorf("cleanup mcp tool calls: %v", err)
		}
	})

	serverID := createTestMCPServer(t, conn, classA, teacherA, "测试服务器-审计", "token-abc")
	if err := InsertToolCalls(ctx, conn, []MCPToolCallInput{
		{
			ClassID: classA, UserID: studentA1, ConversationID: conv, ServerID: &serverID,
			Server: "测试服务器-审计", Tool: "formula_hint", Status: "ok", Round: 1, DurationMS: 12,
		},
		{
			ClassID: classA, UserID: studentA1, ConversationID: conv, ServerID: &serverID,
			Server: "测试服务器-审计", Tool: "unit_convert", Status: "denied", Reason: "not_connected", Round: 0,
		},
	}); err != nil {
		t.Fatalf("InsertToolCalls: %v", err)
	}

	calls, err := ListToolCalls(ctx, conn, classA, 50)
	if err != nil {
		t.Fatalf("ListToolCalls(A): %v", err)
	}
	var ours []MCPToolCall
	for _, call := range calls {
		if call.ConversationID == conv {
			ours = append(ours, call)
		}
	}
	if len(ours) != 2 {
		t.Fatalf("class A calls for conversation %d = %d, want 2", conv, len(ours))
	}
	// Newest first: the denied row was inserted second. Tool is the combined
	// `工具名@服务器名` the spec requires.
	first := ours[0]
	if first.Tool != "unit_convert@测试服务器-审计" || first.Status != "denied" || first.Reason != "not_connected" ||
		first.Round != 0 || first.Username != "student_a1" {
		t.Fatalf("first audit row = %+v", first)
	}

	bCalls, err := ListToolCalls(ctx, conn, classB, 50)
	if err != nil {
		t.Fatalf("ListToolCalls(B): %v", err)
	}
	for _, call := range bCalls {
		if call.ConversationID == conv {
			t.Fatalf("class B saw a class A audit row: %+v", call)
		}
	}

	rows, err := conn.QueryContext(ctx,
		`SELECT COLUMN_NAME FROM information_schema.COLUMNS
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'mcp_tool_calls'`)
	if err != nil {
		t.Fatalf("read audit columns: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan audit column: %v", err)
		}
		lower := strings.ToLower(name)
		if strings.Contains(lower, "param") || strings.Contains(lower, "result") || strings.Contains(lower, "token") {
			t.Errorf("audit table has a forbidden column %q", name)
		}
	}
}

func TestMCPReferencingSkills(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, classB, _ := fixture(t, conn)

	skillID := createTestSkill(t, conn, classA, "测试技能-工具引用")
	if _, err := conn.ExecContext(ctx,
		`UPDATE skills SET tools_json = ? WHERE id = ?`,
		`["knowledge_search","formula_hint@campus-tools"]`, skillID); err != nil {
		t.Fatalf("set tools_json: %v", err)
	}

	names, err := ListSkillsReferencingServer(ctx, conn, classA, "campus-tools")
	if err != nil {
		t.Fatalf("ListSkillsReferencingServer(A): %v", err)
	}
	found := false
	for _, name := range names {
		if name == "测试技能-工具引用" {
			found = true
		}
	}
	if !found {
		t.Fatalf("referencing skills = %v, want the test skill", names)
	}

	names, err = ListSkillsReferencingServer(ctx, conn, classB, "campus-tools")
	if err != nil {
		t.Fatalf("ListSkillsReferencingServer(B): %v", err)
	}
	for _, name := range names {
		if name == "测试技能-工具引用" {
			t.Fatal("class B saw a class A skill reference")
		}
	}
}
