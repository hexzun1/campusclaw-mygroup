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

// TestInsertToolCallsTruncatedRowSharesTheBatch is spec 「调用审计」Scenario
// 超长函数名不影响同轮审计 at the storage layer: a truncated over-long name
// and a normal row land in the same transaction.
func TestInsertToolCallsTruncatedRowSharesTheBatch(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, _, teacherA := fixture(t, conn)
	studentA1 := userIDByUsername(t, conn, "student_a1")

	agent := createTestAgent(t, conn, classA, "测试智能体-截断审计", nil, nil)
	conv := createTestConversation(t, conn, classA, studentA1, agent)
	t.Cleanup(func() {
		if _, err := conn.ExecContext(context.Background(),
			`DELETE FROM mcp_tool_calls WHERE conversation_id = ?`, conv); err != nil {
			t.Errorf("cleanup mcp tool calls: %v", err)
		}
	})

	serverID := createTestMCPServer(t, conn, classA, teacherA, "测试服务器-截断", "token-abc")
	longTool := strings.Repeat("a", 64)
	longServer := strings.Repeat("b", 32)

	if err := InsertToolCalls(ctx, conn, []MCPToolCallInput{
		{
			ClassID: classA, UserID: studentA1, ConversationID: conv, ServerID: &serverID,
			Server: "测试服务器-截断", Tool: "formula_hint", Status: "ok", Round: 1, DurationMS: 7,
		},
		{
			ClassID: classA, UserID: studentA1, ConversationID: conv,
			Server: longServer, Tool: longTool, Status: "denied", Reason: "not_allowed", Round: 1,
		},
	}); err != nil {
		t.Fatalf("InsertToolCalls with a truncated row: %v", err)
	}

	calls, err := ListToolCalls(ctx, conn, classA, 200)
	if err != nil {
		t.Fatalf("ListToolCalls: %v", err)
	}
	var ours []MCPToolCall
	for _, call := range calls {
		if call.ConversationID == conv {
			ours = append(ours, call)
		}
	}
	if len(ours) != 2 {
		t.Fatalf("rows for conversation %d = %d, want 2 (neither row may fail the batch)", conv, len(ours))
	}
	// Newest first: the truncated denied row.
	if !strings.HasPrefix(ours[0].Tool, longTool+"@") || !strings.HasSuffix(ours[0].Tool, longServer) {
		t.Errorf("truncated row tool = %q, want %q@%q", ours[0].Tool, longTool, longServer)
	}
	if ours[0].Status != "denied" || ours[0].Reason != "not_allowed" {
		t.Errorf("truncated row = %+v, want denied/not_allowed", ours[0])
	}
	if ours[1].Tool != "formula_hint@测试服务器-截断" || ours[1].Status != "ok" {
		t.Errorf("normal row = %+v", ours[1])
	}
}

// TestDeleteServerIfUnreferenced is spec 「工具服务器登记与管理」: the check and
// the delete share a transaction, so a referenced server stays and its skills
// are named, while an unreferenced one goes (design D11).
func TestDeleteServerIfUnreferenced(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, _, teacherA := fixture(t, conn)

	serverName := "测试服务器-删除互斥"
	serverID := createTestMCPServer(t, conn, classA, teacherA, serverName, "token-abc")
	skillName := "测试技能-引用删除互斥"
	skillID := createTestSkill(t, conn, classA, skillName)
	if _, err := conn.ExecContext(ctx, `UPDATE skills SET tools_json = ? WHERE id = ?`,
		[]byte(`["formula_hint@`+serverName+`"]`), skillID); err != nil {
		t.Fatalf("point the skill at the server: %v", err)
	}

	names, err := DeleteServerIfUnreferenced(ctx, conn, serverID, classA)
	if err != nil {
		t.Fatalf("DeleteServerIfUnreferenced (referenced): %v", err)
	}
	if len(names) != 1 || names[0] != skillName {
		t.Errorf("referencing skills = %v, want [%s]", names, skillName)
	}
	if _, err := GetServer(ctx, conn, serverID, classA); err != nil {
		t.Errorf("the referenced server was deleted: %v", err)
	}

	if _, err := conn.ExecContext(ctx, `UPDATE skills SET tools_json = ? WHERE id = ?`, []byte(`[]`), skillID); err != nil {
		t.Fatalf("clear the reference: %v", err)
	}

	names, err = DeleteServerIfUnreferenced(ctx, conn, serverID, classA)
	if err != nil {
		t.Fatalf("DeleteServerIfUnreferenced (unreferenced): %v", err)
	}
	if len(names) != 0 {
		t.Errorf("referencing skills = %v, want none", names)
	}
	if _, err := GetServer(ctx, conn, serverID, classA); !errors.Is(err, ErrNotFound) {
		t.Errorf("server still exists after delete: %v", err)
	}

	// A missing row and another class's row answer the same way.
	if _, err := DeleteServerIfUnreferenced(ctx, conn, serverID, classA); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
}

// TestSkillServerLock is spec 「技能对 MCP 工具的引用校验」Scenario 保存期间服务器
// 被删除: the reference is re-checked inside the skill's own transaction, so a
// server deleted after the caller's outside check blocks the save with
// ErrSkillServerMissing and writes nothing (design D11).
func TestSkillServerLock(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, _, teacherA := fixture(t, conn)

	serverName := "测试服务器-保存加锁"
	serverID := createTestMCPServer(t, conn, classA, teacherA, serverName, "token-abc")
	if _, err := conn.ExecContext(ctx,
		`UPDATE mcp_servers SET status = ? WHERE id = ?`, MCPServerConnected, serverID); err != nil {
		t.Fatalf("mark the server connected: %v", err)
	}
	refs := []SkillServerRef{{Name: serverName}}

	// Happy path: a connected server is accepted, so the re-check is not what
	// rejects every save.
	okName := "测试技能-保存加锁-正常"
	okID, err := CreateSkill(ctx, conn, Skill{
		ClassID: classA, Name: okName, TriggerText: "测试，test",
		ToolsJSON:   []byte(`["formula_hint@` + serverName + `"]`),
		Description: "测试用技能说明。", SourceText: "名称：" + okName, Enabled: true,
	}, refs)
	if err != nil {
		t.Fatalf("CreateSkill against a connected server: %v", err)
	}
	t.Cleanup(func() {
		if _, err := conn.ExecContext(context.Background(), `DELETE FROM skills WHERE id = ?`, okID); err != nil {
			t.Errorf("cleanup skill %d: %v", okID, err)
		}
	})

	// The outside check passed, then the server went away.
	if err := DeleteServer(ctx, conn, serverID, classA); err != nil {
		t.Fatalf("delete the server after the outside check: %v", err)
	}

	missingName := "测试技能-保存加锁-已删"
	_, err = CreateSkill(ctx, conn, Skill{
		ClassID: classA, Name: missingName, TriggerText: "测试，test",
		ToolsJSON:   []byte(`["formula_hint@` + serverName + `"]`),
		Description: "测试用技能说明。", SourceText: "名称：" + missingName, Enabled: true,
	}, refs)
	var missing ErrSkillServerMissing
	if !errors.As(err, &missing) || missing.Name != serverName {
		t.Fatalf("CreateSkill after the delete = %v, want ErrSkillServerMissing{%s}", err, serverName)
	}
	var written int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM skills WHERE class_id = ? AND name = ?`, classA, missingName).Scan(&written); err != nil {
		t.Fatalf("count the rejected skill: %v", err)
	}
	if written != 0 {
		t.Errorf("the rejected skill was written (%d rows); the transaction must roll back", written)
	}

	// A server that exists but is not connected is refused too: the tool loop
	// could only deny such a reference at run time.
	offlineName := "测试服务器-保存未连接"
	offlineID := createTestMCPServer(t, conn, classA, teacherA, offlineName, "token-abc")
	offlineSkill := "测试技能-保存未连接"
	if _, err := CreateSkill(ctx, conn, Skill{
		ClassID: classA, Name: offlineSkill, TriggerText: "测试，test",
		ToolsJSON:   []byte(`["formula_hint@` + offlineName + `"]`),
		Description: "测试用技能说明。", SourceText: "名称：" + offlineSkill, Enabled: true,
	}, []SkillServerRef{{Name: offlineName}}); !errors.As(err, new(ErrSkillServerNotConnected)) {
		t.Errorf("CreateSkill against a disconnected server = %v, want ErrSkillServerNotConnected (id %d)", err, offlineID)
	}

	// UpdateSkill takes the same check and leaves the old row untouched.
	otherName := "测试技能-保存加锁-更新"
	otherID := createTestSkill(t, conn, classA, otherName)
	err = UpdateSkill(ctx, conn, Skill{
		ID: otherID, ClassID: classA, Name: otherName, TriggerText: "改过的，changed",
		ToolsJSON:   []byte(`["formula_hint@` + serverName + `"]`),
		Description: "改过的说明。", SourceText: "名称：" + otherName, Enabled: true,
	}, refs)
	if !errors.As(err, new(ErrSkillServerMissing)) {
		t.Fatalf("UpdateSkill after the delete = %v, want ErrSkillServerMissing", err)
	}
	stored, err := GetSkill(ctx, conn, otherID, classA)
	if err != nil {
		t.Fatalf("re-read the untouched skill: %v", err)
	}
	if stored.TriggerText != "测试，test" || string(stored.ToolsJSON) != "[]" {
		t.Errorf("rejected update changed the row: %+v", stored)
	}
}
