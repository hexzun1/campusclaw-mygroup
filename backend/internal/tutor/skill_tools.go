package tutor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"campusclaw/backend/internal/db"
)

const maxMCPFunctionNameLen = 64

// invalidSkillToolError marks a reference the teacher can fix; handlers turn
// it into 400 while a wrapped database failure stays a 503.
type invalidSkillToolError struct {
	message string
}

func (e *invalidSkillToolError) Error() string { return e.message }

func invalidSkillTool(format string, args ...any) error {
	return &invalidSkillToolError{message: fmt.Sprintf(format, args...)}
}

// validateSkillMCPTools checks every `tool@server` reference at save time
// (spec: 技能对 MCP 工具的引用校验). A reference to another class's server is
// reported with the same wording as a nonexistent one, so existence does not
// leak. Runtime disconnects after saving are handled by the tool loop.
func validateSkillMCPTools(ctx context.Context, conn *sql.DB, classID int, tools []string) error {
	for _, ref := range tools {
		tool, serverName, ok := splitMCPToolRef(ref)
		if !ok {
			continue
		}
		if len(tool)+len("__")+len(serverName) > maxMCPFunctionNameLen {
			return invalidSkillTool(
				"字段“工具”的取值“%s”映射后的函数名“%s__%s”超过 %d 个字符",
				ref, tool, serverName, maxMCPFunctionNameLen)
		}

		server, err := db.GetServerByName(ctx, conn, classID, serverName)
		if errors.Is(err, db.ErrNotFound) {
			return invalidSkillTool("字段“工具”的取值“%s”：服务器“%s”不存在", ref, serverName)
		}
		if err != nil {
			return fmt.Errorf("lookup mcp server %q: %w", serverName, err)
		}
		if server.Status != db.MCPServerConnected {
			return invalidSkillTool(
				"字段“工具”的取值“%s”：服务器“%s”未连接，请先在工具服务器页连接",
				ref, serverName)
		}

		cached, err := db.ListServerTools(ctx, conn, server.ID, classID)
		if err != nil {
			return fmt.Errorf("list tools of mcp server %q: %w", serverName, err)
		}
		found := false
		for _, t := range cached {
			if t.Name == tool {
				found = true
				break
			}
		}
		if !found {
			return invalidSkillTool(
				"字段“工具”的取值“%s”：工具“%s”不在服务器“%s”的工具清单中，请重新连接刷新",
				ref, tool, serverName)
		}
	}
	return nil
}
