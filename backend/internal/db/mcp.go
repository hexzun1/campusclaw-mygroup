package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MCP server status values, matching the ENUM in 004_mcp.sql.
const (
	MCPServerDisconnected = "disconnected"
	MCPServerConnected    = "connected"
	MCPServerError        = "error"
)

// MCPServer is a server row without its token. HasToken is the only token
// signal exposed to callers; the value itself is read solely by
// GetServerToken at the moment a request is sent (design.md Decision 5).
type MCPServer struct {
	ID               int        `json:"id"`
	Name             string     `json:"name"`
	BaseURL          string     `json:"base_url"`
	HasToken         bool       `json:"has_token"`
	Status           string     `json:"status"`
	ToolCount        int        `json:"tool_count"`
	LastError        string     `json:"last_error"`
	ToolsRefreshedAt *time.Time `json:"tools_refreshed_at"`
}

// MCPTool is one cached tool discovery result.
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// MCPToolCall is one audit row as returned by the teacher-facing list. Tool
// carries the combined `工具名@服务器名` form required by the spec, so it is
// directly comparable with meta.tool_calls; the separate tool and server
// columns stay in the table (and in the audit insert) as the storage shape.
type MCPToolCall struct {
	ID             int64     `json:"id"`
	Tool           string    `json:"tool"`
	Status         string    `json:"status"`
	Reason         string    `json:"reason,omitempty"`
	Username       string    `json:"username"`
	ConversationID int       `json:"conversation_id"`
	Round          int       `json:"round"`
	DurationMS     int64     `json:"duration_ms"`
	CreatedAt      time.Time `json:"created_at"`
}

// MCPToolCallInput is one audit row to insert. It deliberately has no
// arguments, results or token fields.
type MCPToolCallInput struct {
	ClassID        int
	UserID         int
	ConversationID int
	ServerID       *int
	Server         string
	Tool           string
	Status         string
	Reason         string
	Round          int
	DurationMS     int64
}

const mcpServerSelect = `SELECT s.id, s.name, s.base_url, s.status, s.last_error, s.tools_refreshed_at,
       (SELECT COUNT(*) FROM mcp_tools t WHERE t.server_id = s.id),
       (s.auth_token IS NOT NULL AND LENGTH(s.auth_token) > 0)
FROM mcp_servers s`

func scanMCPServer(row interface{ Scan(...any) error }) (*MCPServer, error) {
	var (
		s         MCPServer
		lastError sql.NullString
		refreshed sql.NullTime
	)
	if err := row.Scan(&s.ID, &s.Name, &s.BaseURL, &s.Status, &lastError, &refreshed, &s.ToolCount, &s.HasToken); err != nil {
		return nil, err
	}
	s.LastError = lastError.String
	if refreshed.Valid {
		t := refreshed.Time
		s.ToolsRefreshedAt = &t
	}
	return &s, nil
}

// ListServers returns the class's servers ordered by id. The projection never
// selects auth_token.
func ListServers(ctx context.Context, conn *sql.DB, classID int) ([]MCPServer, error) {
	rows, err := conn.QueryContext(ctx, mcpServerSelect+` WHERE s.class_id = ? ORDER BY s.id`, classID)
	if err != nil {
		return nil, fmt.Errorf("list mcp servers: %w", err)
	}
	defer rows.Close()

	out := []MCPServer{}
	for rows.Next() {
		s, err := scanMCPServer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan mcp server: %w", err)
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// GetServer reads one server by id and class. A row from another class is
// ErrNotFound, exactly like a missing id.
func GetServer(ctx context.Context, conn *sql.DB, id, classID int) (*MCPServer, error) {
	s, err := scanMCPServer(conn.QueryRowContext(ctx,
		mcpServerSelect+` WHERE s.id = ? AND s.class_id = ?`, id, classID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get mcp server: %w", err)
	}
	return s, nil
}

// GetServerByName is the class-scoped lookup used when resolving a skill's
// `tool@server` reference.
func GetServerByName(ctx context.Context, conn *sql.DB, classID int, name string) (*MCPServer, error) {
	s, err := scanMCPServer(conn.QueryRowContext(ctx,
		mcpServerSelect+` WHERE s.class_id = ? AND s.name = ?`, classID, name))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get mcp server by name: %w", err)
	}
	return s, nil
}

// GetServerBySeedKey supports idempotent seeding without overwriting edits.
func GetServerBySeedKey(ctx context.Context, conn *sql.DB, classID int, seedKey string) (*MCPServer, error) {
	s, err := scanMCPServer(conn.QueryRowContext(ctx,
		mcpServerSelect+` WHERE s.class_id = ? AND s.seed_key = ?`, classID, seedKey))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get mcp server by seed key: %w", err)
	}
	return s, nil
}

// GetServerToken is the single read path for the stored token.
func GetServerToken(ctx context.Context, conn *sql.DB, id, classID int) (string, error) {
	var token []byte
	err := conn.QueryRowContext(ctx,
		`SELECT auth_token FROM mcp_servers WHERE id = ? AND class_id = ?`, id, classID).Scan(&token)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get mcp server token: %w", err)
	}
	return string(token), nil
}

// CreateServer inserts a disconnected server. A duplicate (class, name) is
// ErrDuplicateName, which handlers map to 409.
func CreateServer(ctx context.Context, conn *sql.DB, classID int, name, baseURL, token string, createdBy int, seedKey string) (int, error) {
	res, err := conn.ExecContext(ctx,
		`INSERT INTO mcp_servers (class_id, name, base_url, auth_token, status, created_by, seed_key)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		classID, name, baseURL, []byte(token), MCPServerDisconnected, createdBy, nullableString(seedKey))
	if err != nil {
		if isDuplicateKeyErr(err) {
			return 0, ErrDuplicateName
		}
		return 0, fmt.Errorf("insert mcp server: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("mcp server id: %w", err)
	}
	return int(id), nil
}

// ReplaceServerTools replaces the whole cache and marks the server connected
// in one transaction: readers never see a half-refreshed list (design.md
// Decision 6).
func ReplaceServerTools(ctx context.Context, conn *sql.DB, serverID, classID int, tools []MCPTool) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace mcp tools: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockServer(ctx, tx, serverID, classID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM mcp_tools WHERE server_id = ?`, serverID); err != nil {
		return fmt.Errorf("clear mcp tools: %w", err)
	}
	for _, tool := range tools {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO mcp_tools (server_id, name, description, input_schema) VALUES (?, ?, ?, ?)`,
			serverID, tool.Name, tool.Description, []byte(tool.InputSchema)); err != nil {
			return fmt.Errorf("insert mcp tool %s: %w", tool.Name, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE mcp_servers SET status = ?, last_error = NULL, tools_refreshed_at = NOW()
		 WHERE id = ? AND class_id = ?`, MCPServerConnected, serverID, classID); err != nil {
		return fmt.Errorf("mark mcp server connected: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace mcp tools: %w", err)
	}
	return nil
}

// MarkServerError clears the cache and records a token-free last_error.
func MarkServerError(ctx context.Context, conn *sql.DB, serverID, classID int, lastError string) error {
	return updateServerState(ctx, conn, serverID, classID,
		MCPServerError, nullableString(lastError), true)
}

// DisconnectServer clears the cache and returns the server to disconnected.
func DisconnectServer(ctx context.Context, conn *sql.DB, serverID, classID int) error {
	return updateServerState(ctx, conn, serverID, classID,
		MCPServerDisconnected, nil, true)
}

func updateServerState(ctx context.Context, conn *sql.DB, serverID, classID int, status string, lastError any, clearCache bool) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update mcp server: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockServer(ctx, tx, serverID, classID); err != nil {
		return err
	}
	if clearCache {
		if _, err := tx.ExecContext(ctx, `DELETE FROM mcp_tools WHERE server_id = ?`, serverID); err != nil {
			return fmt.Errorf("clear mcp tools: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE mcp_servers SET status = ?, last_error = ?, tools_refreshed_at = NULL
		 WHERE id = ? AND class_id = ?`, status, lastError, serverID, classID); err != nil {
		return fmt.Errorf("update mcp server state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update mcp server: %w", err)
	}
	return nil
}

func lockServer(ctx context.Context, tx *sql.Tx, serverID, classID int) error {
	var id int
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM mcp_servers WHERE id = ? AND class_id = ? FOR UPDATE`, serverID, classID).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock mcp server: %w", err)
	}
	return nil
}

// ListServerTools returns the cached tools for one class-scoped server.
func ListServerTools(ctx context.Context, conn *sql.DB, serverID, classID int) ([]MCPTool, error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT t.name, t.description, t.input_schema
		 FROM mcp_tools t JOIN mcp_servers s ON s.id = t.server_id
		 WHERE t.server_id = ? AND s.class_id = ?
		 ORDER BY t.name`, serverID, classID)
	if err != nil {
		return nil, fmt.Errorf("list mcp tools: %w", err)
	}
	defer rows.Close()

	out := []MCPTool{}
	for rows.Next() {
		var (
			tool   MCPTool
			schema []byte
		)
		if err := rows.Scan(&tool.Name, &tool.Description, &schema); err != nil {
			return nil, fmt.Errorf("scan mcp tool: %w", err)
		}
		tool.InputSchema = json.RawMessage(schema)
		out = append(out, tool)
	}
	return out, rows.Err()
}

// DeleteServer removes one class-scoped server. mcp_tools cascades; audit rows
// keep their server/tool names and lose server_id.
func DeleteServer(ctx context.Context, conn *sql.DB, id, classID int) error {
	res, err := conn.ExecContext(ctx, `DELETE FROM mcp_servers WHERE id = ? AND class_id = ?`, id, classID)
	if err != nil {
		return fmt.Errorf("delete mcp server: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete mcp server rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteServerIfUnreferenced deletes a server only when no class skill still
// references it, and does the reference check inside the same transaction that
// deletes. Returns the referencing skill names when it refuses.
//
// Lock order is server first, then skills — the same order CreateSkill and
// UpdateSkill take, so a save and a delete cannot deadlock (design.md
// Decision 11).
func DeleteServerIfUnreferenced(ctx context.Context, conn *sql.DB, id, classID int) ([]string, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin delete server tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var name string
	if err := tx.QueryRowContext(ctx,
		`SELECT name FROM mcp_servers WHERE id = ? AND class_id = ? FOR UPDATE`,
		id, classID).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock mcp server: %w", err)
	}

	names, err := referencingSkills(ctx, tx, classID, name)
	if err != nil {
		return nil, err
	}
	if len(names) > 0 {
		return names, nil
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM mcp_servers WHERE id = ? AND class_id = ?`, id, classID); err != nil {
		return nil, fmt.Errorf("delete mcp server: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit delete server tx: %w", err)
	}
	return nil, nil
}

// referencingSkills returns the class's skills whose tools_json names the
// server. The read takes FOR SHARE: under REPEATABLE READ a plain read could
// return a snapshot from before a concurrent save committed (design.md
// Decision 11).
func referencingSkills(ctx context.Context, tx *sql.Tx, classID int, serverName string) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT name, tools_json FROM skills WHERE class_id = ? ORDER BY id FOR SHARE`, classID)
	if err != nil {
		return nil, fmt.Errorf("list skills for mcp reference: %w", err)
	}
	defer rows.Close()

	suffix := "@" + serverName
	var names []string
	for rows.Next() {
		var (
			name  string
			tools []byte
		)
		if err := rows.Scan(&name, &tools); err != nil {
			return nil, fmt.Errorf("scan skill for mcp reference: %w", err)
		}
		var refs []string
		if err := json.Unmarshal(tools, &refs); err != nil {
			continue
		}
		for _, ref := range refs {
			if strings.HasSuffix(ref, suffix) {
				names = append(names, name)
				break
			}
		}
	}
	return names, rows.Err()
}

// ListSkillsReferencingServer returns the names of class skills whose
// tools_json contains an exact `tool@serverName` reference. It is the
// read-only form used where no deletion follows; the delete path uses
// DeleteServerIfUnreferenced so the check cannot go stale.
func ListSkillsReferencingServer(ctx context.Context, conn *sql.DB, classID int, serverName string) ([]string, error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT name, tools_json FROM skills WHERE class_id = ? ORDER BY id`, classID)
	if err != nil {
		return nil, fmt.Errorf("list skills for mcp reference: %w", err)
	}
	defer rows.Close()

	suffix := "@" + serverName
	var names []string
	for rows.Next() {
		var (
			name  string
			tools []byte
		)
		if err := rows.Scan(&name, &tools); err != nil {
			return nil, fmt.Errorf("scan skill for mcp reference: %w", err)
		}
		var refs []string
		if err := json.Unmarshal(tools, &refs); err != nil {
			continue
		}
		for _, ref := range refs {
			if strings.HasSuffix(ref, suffix) {
				names = append(names, name)
				break
			}
		}
	}
	return names, rows.Err()
}

// InsertToolCalls writes the turn's audit records in one transaction. It is
// called before the stream meta is written, so the audit survives a later
// generation failure (design.md Decision 8).
func InsertToolCalls(ctx context.Context, conn *sql.DB, calls []MCPToolCallInput) error {
	if len(calls) == 0 {
		return nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin insert mcp tool calls: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, call := range calls {
		var serverID any
		if call.ServerID != nil {
			serverID = *call.ServerID
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO mcp_tool_calls
			 (class_id, user_id, conversation_id, server_id, server, tool, status, reason, `+"`round`"+`, duration_ms)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			call.ClassID, call.UserID, call.ConversationID, serverID, call.Server, call.Tool,
			call.Status, nullableString(call.Reason), call.Round, call.DurationMS); err != nil {
			return fmt.Errorf("insert mcp tool call: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mcp tool calls: %w", err)
	}
	return nil
}

// ListToolCalls returns the class's audit rows, newest first. The spec wants
// each row to carry `工具名@服务器名`, so the two columns are combined here
// rather than exposed separately.
func ListToolCalls(ctx context.Context, conn *sql.DB, classID, limit int) ([]MCPToolCall, error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT c.id, CONCAT(c.tool, '@', c.server), c.status, COALESCE(c.reason, ''), u.username,
		        c.conversation_id, c.`+"`round`"+`, c.duration_ms, c.created_at
		 FROM mcp_tool_calls c JOIN users u ON u.id = c.user_id
		 WHERE c.class_id = ?
		 ORDER BY c.id DESC
		 LIMIT ?`, classID, limit)
	if err != nil {
		return nil, fmt.Errorf("list mcp tool calls: %w", err)
	}
	defer rows.Close()

	out := []MCPToolCall{}
	for rows.Next() {
		var call MCPToolCall
		if err := rows.Scan(&call.ID, &call.Tool, &call.Status, &call.Reason,
			&call.Username, &call.ConversationID, &call.Round, &call.DurationMS, &call.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan mcp tool call: %w", err)
		}
		out = append(out, call)
	}
	return out, rows.Err()
}
