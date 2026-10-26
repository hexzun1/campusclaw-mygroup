package tutor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
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
			return invalidSkillTool("字段“工具”的取值“%s”：%s", ref, skillServerMissingText(serverName))
		}
		if err != nil {
			return fmt.Errorf("lookup mcp server %q: %w", serverName, err)
		}
		if server.Status != db.MCPServerConnected {
			return invalidSkillTool("字段“工具”的取值“%s”：%s", ref, skillServerNotConnectedText(serverName))
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

// skillServerRefs turns a skill's tools_json into the deduped, name-sorted list
// of MCP servers the save must lock. Sorting makes two concurrent saves of the
// same content take the same locks in the same order (design.md Decision 11).
func skillServerRefs(toolsJSON []byte) []db.SkillServerRef {
	seen := map[string]bool{}
	var refs []db.SkillServerRef
	for _, ref := range unmarshalTools(toolsJSON) {
		_, serverName, ok := splitMCPToolRef(ref)
		if !ok || seen[serverName] {
			continue
		}
		seen[serverName] = true
		refs = append(refs, db.SkillServerRef{Name: serverName})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	return refs
}

// skillServerMissingText and skillServerNotConnectedText are the two reference
// failures the outside check and the in-transaction re-check can both raise.
// Sharing the wording keeps a save that loses the race to a delete reading
// exactly like any other bad reference (spec: 技能对 MCP 工具的引用校验).
func skillServerMissingText(serverName string) string {
	return fmt.Sprintf("服务器“%s”不存在，请先登记并连接", serverName)
}

func skillServerNotConnectedText(serverName string) string {
	return fmt.Sprintf("服务器“%s”未连接，请先在工具服务器页连接", serverName)
}

// writeSkillSaveError maps the in-transaction re-check failures to the same
// 400 wording the outside validation uses, so a save racing a delete reads like
// any other bad reference (spec: 技能对 MCP 工具的引用校验).
func writeSkillSaveError(w http.ResponseWriter, err error) {
	var missing db.ErrSkillServerMissing
	if errors.As(err, &missing) {
		httpapi.WriteError(w, http.StatusBadRequest,
			fmt.Sprintf("字段“工具”的取值：%s", skillServerMissingText(missing.Name)))
		return
	}
	var notConnected db.ErrSkillServerNotConnected
	if errors.As(err, &notConnected) {
		httpapi.WriteError(w, http.StatusBadRequest,
			fmt.Sprintf("字段“工具”的取值：%s", skillServerNotConnectedText(notConnected.Name)))
		return
	}
	writeWriteError(w, err, "已存在同名技能")
}
