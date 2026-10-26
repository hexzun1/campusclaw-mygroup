// Package tutor implements the conversational tutor domain: skill-file
// parsing, lexical skill routing, retrieval post-processing, gateway message
// assembly, citation output filtering, the streaming conversation orchestrator
// and the HTTP handlers for agents, skills and conversations (design.md
// Decisions 3-7). Everything except the orchestrator and the handlers is a
// pure function, testable without a database or a gateway.
package tutor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"campusclaw/backend/internal/db"
)

const (
	maxSkillFileRunes = 8000
	maxSkillNameRunes = 32
	maxTriggerRunes   = 120
	maxSubjectRunes   = 32
	maxBodyRunes      = 2000
	maxSkillTools     = 3
)

// toolKnowledgeSearch is the built-in search tool a skill may name directly.
// It is validated and displayed, never sent to the gateway as a tool
// definition; only MCP references become function definitions (design.md
// Decision 7).
const toolKnowledgeSearch = "knowledge_search"

var (
	mcpRefToolNameRE   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	mcpRefServerNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// skillFieldNames lists the fields a key line may define.
var skillFieldNames = []string{"名称", "适用条件", "资料", "工具", "启用"}

// ParseSkill parses one skill file (design.md Decision 3). It returns only the
// first error, each message naming the offending line number or field, so a
// teacher can fix the file one step at a time. The returned Skill has no ID,
// ClassID or SeedKey: the caller assigns those.
func ParseSkill(text string) (db.Skill, error) {
	if utf8.RuneCountInString(text) > maxSkillFileRunes {
		return db.Skill{}, fmt.Errorf("技能文件不能超过 %d 个字符", maxSkillFileRunes)
	}

	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	fields := make(map[string]string)
	keySeen := false
	bodyStart := -1 // index into lines of the first body line; -1 = no separator
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") {
			continue // comment lines are ignored across the key region
		}
		if line == "" {
			if !keySeen {
				continue // blank lines before the first key line are ignored
			}
			bodyStart = i + 1
			break
		}

		key, value, ok := splitKeyLine(line)
		if !ok {
			return db.Skill{}, fmt.Errorf("第 %d 行：不是“字段：值”格式", i+1)
		}
		if !knownSkillField(key) {
			return db.Skill{}, fmt.Errorf("第 %d 行：未定义的字段“%s”，可用字段为 %s", i+1, key, strings.Join(skillFieldNames, "、"))
		}
		if _, dup := fields[key]; dup {
			return db.Skill{}, fmt.Errorf("第 %d 行：字段“%s”重复", i+1, key)
		}
		fields[key] = value
		keySeen = true
	}

	var body string
	if bodyStart >= 0 {
		body = strings.TrimSpace(strings.Join(lines[bodyStart:], "\n"))
	}
	if body == "" {
		if bodyStart < 0 {
			return db.Skill{}, fmt.Errorf("第 %d 行：字段之后缺少空行与说明正文", len(lines))
		}
		return db.Skill{}, fmt.Errorf("第 %d 行：空行以下缺少说明正文", bodyStart)
	}
	if utf8.RuneCountInString(body) > maxBodyRunes {
		return db.Skill{}, fmt.Errorf("说明正文不能超过 %d 个字符", maxBodyRunes)
	}

	name, err := requiredSkillField(fields, "名称")
	if err != nil {
		return db.Skill{}, err
	}
	if utf8.RuneCountInString(name) > maxSkillNameRunes {
		return db.Skill{}, fmt.Errorf("字段“名称”不能超过 %d 个字符", maxSkillNameRunes)
	}

	trigger, err := requiredSkillField(fields, "适用条件")
	if err != nil {
		return db.Skill{}, err
	}
	if utf8.RuneCountInString(trigger) > maxTriggerRunes {
		return db.Skill{}, fmt.Errorf("字段“适用条件”不能超过 %d 个字符", maxTriggerRunes)
	}

	subject := fields["资料"]
	if utf8.RuneCountInString(subject) > maxSubjectRunes {
		return db.Skill{}, fmt.Errorf("字段“资料”不能超过 %d 个字符", maxSubjectRunes)
	}

	tools := []string{}
	if v, ok := fields["工具"]; ok {
		parsed, err := parseSkillTools(v)
		if err != nil {
			return db.Skill{}, err
		}
		tools = parsed
	}
	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		return db.Skill{}, fmt.Errorf("字段“工具”无法保存：%v", err)
	}

	enabledText, err := requiredSkillField(fields, "启用")
	if err != nil {
		return db.Skill{}, err
	}
	var enabled bool
	switch enabledText {
	case "是":
		enabled = true
	case "否":
		enabled = false
	default:
		return db.Skill{}, fmt.Errorf("字段“启用”只能是“是”或“否”")
	}

	return db.Skill{
		Name:        name,
		TriggerText: trigger,
		SubjectText: subject,
		ToolsJSON:   toolsJSON,
		Description: body,
		SourceText:  text,
		Enabled:     enabled,
	}, nil
}

// parseSkillTools parses the 工具 field: 1–3 comma-separated values, each
// either knowledge_search or a `tool@server` MCP reference. Commas may be
// full-width or half-width. Values keep the order given (design.md Decision 4).
func parseSkillTools(raw string) ([]string, error) {
	parts := strings.Split(strings.ReplaceAll(raw, "，", ","), ",")
	if len(parts) > maxSkillTools {
		return nil, fmt.Errorf("字段“工具”最多只能有 %d 个取值，实际给了 %d 个：%s",
			maxSkillTools, len(parts), strings.TrimSpace(raw))
	}
	seen := make(map[string]bool, len(parts))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != toolKnowledgeSearch {
			if _, _, ok := splitMCPToolRef(value); !ok {
				return nil, fmt.Errorf(
					"字段“工具”只能是 %s 或“工具名@服务器名”，取值“%s”不合法",
					toolKnowledgeSearch, value)
			}
		}
		if seen[value] {
			return nil, fmt.Errorf("字段“工具”的取值“%s”重复", value)
		}
		seen[value] = true
		out = append(out, value)
	}
	return out, nil
}

// splitMCPToolRef splits a `tool@server` reference and checks both halves
// against the name patterns shared by mcp-server, registration and discovery.
func splitMCPToolRef(value string) (tool, server string, ok bool) {
	if strings.Count(value, "@") != 1 {
		return "", "", false
	}
	tool, server, _ = strings.Cut(value, "@")
	if !mcpRefToolNameRE.MatchString(tool) || !mcpRefServerNameRE.MatchString(server) {
		return "", "", false
	}
	return tool, server, true
}

// splitKeyLine splits a key line at its first full-width or half-width colon.
func splitKeyLine(line string) (key, value string, ok bool) {
	idx := strings.IndexAny(line, "：:")
	if idx < 0 {
		return "", "", false
	}
	_, size := utf8.DecodeRuneInString(line[idx:])
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+size:])
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

func knownSkillField(key string) bool {
	switch key {
	case "名称", "适用条件", "资料", "工具", "启用":
		return true
	}
	return false
}

func requiredSkillField(fields map[string]string, key string) (string, error) {
	v := fields[key]
	if v == "" {
		return "", fmt.Errorf("缺少必填字段“%s”", key)
	}
	return v, nil
}
