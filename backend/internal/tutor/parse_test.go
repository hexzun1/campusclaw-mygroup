package tutor

import (
	"strings"
	"testing"
)

const validSkillFile = `# 数学技能
名称：数学解题引导
适用条件：数学，方程、计算；math
资料：数学
工具：knowledge_search
启用：是

先判对错，再引导思路。
保持 # 号与

空行原样。`

func TestParseSkillAcceptsValidFile(t *testing.T) {
	s, err := ParseSkill(validSkillFile)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}

	if s.Name != "数学解题引导" {
		t.Errorf("Name = %q", s.Name)
	}
	if s.TriggerText != "数学，方程、计算；math" {
		t.Errorf("TriggerText = %q", s.TriggerText)
	}
	if s.SubjectText != "数学" {
		t.Errorf("SubjectText = %q", s.SubjectText)
	}
	if string(s.ToolsJSON) != `["knowledge_search"]` {
		t.Errorf("ToolsJSON = %s", s.ToolsJSON)
	}
	if !s.Enabled {
		t.Error("Enabled = false, want true")
	}
	if s.Description != "先判对错，再引导思路。\n保持 # 号与\n\n空行原样。" {
		t.Errorf("Description = %q: interior # and blank lines must survive verbatim", s.Description)
	}
	if s.SourceText != validSkillFile {
		t.Error("SourceText must be the file verbatim")
	}
}

func TestParseSkillOptionalFieldsAndColonStyles(t *testing.T) {
	// Half-width colons, no 资料 and no 工具, CRLF line endings.
	file := "名称:语文解题引导\r\n适用条件: 语文，作文\r\n启用: 是\r\n\r\n阅读题干再作答。"
	s, err := ParseSkill(file)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}

	if s.Name != "语文解题引导" || s.TriggerText != "语文，作文" || !s.Enabled {
		t.Errorf("parsed fields wrong: %+v", s)
	}
	if s.SubjectText != "" {
		t.Errorf("SubjectText = %q, want empty", s.SubjectText)
	}
	if string(s.ToolsJSON) != `[]` {
		t.Errorf("ToolsJSON = %s, want []", s.ToolsJSON)
	}
	if s.Description != "阅读题干再作答。" {
		t.Errorf("Description = %q", s.Description)
	}
	if s.SourceText != file {
		t.Error("SourceText must keep the original CRLF text")
	}
}

func TestParseSkillToolFieldAcceptsOneToThreeValues(t *testing.T) {
	file := "名称：物理\n适用条件：物理\n工具：knowledge_search，formula_hint@campus-tools, unit_convert@campus-tools\n启用：是\n\n正文"
	s, err := ParseSkill(file)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	want := `["knowledge_search","formula_hint@campus-tools","unit_convert@campus-tools"]`
	if string(s.ToolsJSON) != want {
		t.Errorf("ToolsJSON = %s, want %s", s.ToolsJSON, want)
	}

	single := "名称：数学\n适用条件：数学\n工具： formula_hint@campus-tools \n启用：是\n\n正文"
	s, err = ParseSkill(single)
	if err != nil {
		t.Fatalf("ParseSkill single: %v", err)
	}
	if string(s.ToolsJSON) != `["formula_hint@campus-tools"]` {
		t.Errorf("ToolsJSON = %s: surrounding spaces must be trimmed", s.ToolsJSON)
	}
}

func TestParseSkillToolFieldErrors(t *testing.T) {
	cases := []struct {
		name  string
		tools string
		want  string
	}{
		{"four values", "knowledge_search,a@campus-tools,b@campus-tools,c@campus-tools", "字段“工具”最多只能有 3 个取值"},
		{"duplicate", "knowledge_search，knowledge_search", "取值“knowledge_search”重复"},
		{"tool name uppercase", "Formula@campus-tools", "取值“Formula@campus-tools”不合法"},
		{"missing server", "formula_hint@", "取值“formula_hint@”不合法"},
		{"server uppercase", "formula_hint@Campus_Tools", "取值“formula_hint@Campus_Tools”不合法"},
		{"unknown built-in", "web_search", "取值“web_search”不合法"},
		{"double at", "formula_hint@@campus-tools", "不合法"},
		{"empty", "", "取值“”不合法"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := "名称：甲\n适用条件：x\n工具：" + tc.tools + "\n启用：是\n\n正文"
			_, err := ParseSkill(file)
			if err == nil {
				t.Fatal("ParseSkill succeeded, want an error")
			}
			if !strings.Contains(err.Error(), "字段“工具”") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to name 工具 and contain %q", err, tc.want)
			}
		})
	}
}

func TestParseSkillErrorMatrix(t *testing.T) {
	cases := []struct {
		name string
		file string
		want string // exact substring the Chinese message must contain
	}{
		{"duplicate field", "名称：甲\n名称：乙\n适用条件：x\n启用：是\n\n正文",
			"第 2 行：字段“名称”重复"},
		{"undefined field", "名称：甲\n作者：张老师\n适用条件：x\n启用：是\n\n正文",
			"第 2 行：未定义的字段“作者”"},
		{"line without colon", "名称：甲\n这是正文\n适用条件：x\n启用：是\n\n正文",
			"第 2 行：不是“字段：值”格式"},
		{"tool not knowledge_search", "名称：甲\n适用条件：x\n工具：web_search\n启用：是\n\n正文",
			"字段“工具”只能是 knowledge_search"},
		{"tool empty", "名称：甲\n适用条件：x\n工具：\n启用：是\n\n正文",
			"字段“工具”只能是 knowledge_search"},
		{"missing trigger", "名称：甲\n启用：是\n\n正文",
			"缺少必填字段“适用条件”"},
		{"missing name", "适用条件：x\n启用：是\n\n正文",
			"缺少必填字段“名称”"},
		{"missing enabled", "名称：甲\n适用条件：x\n\n正文",
			"缺少必填字段“启用”"},
		{"enabled neither", "名称：甲\n适用条件：x\n启用：可能\n\n正文",
			"字段“启用”只能是“是”或“否”"},
		{"no separator, no body", "名称：甲\n适用条件：x\n启用：是",
			"缺少空行与说明正文"},
		{"blank after separator only", "名称：甲\n适用条件：x\n启用：是\n\n\n",
			"空行以下缺少说明正文"},
		{"name 33 runes", "名称：" + strings.Repeat("字", 33) + "\n适用条件：x\n启用：是\n\n正文",
			"字段“名称”不能超过 32 个字符"},
		{"body 2001 runes", "名称：甲\n适用条件：x\n启用：是\n\n" + strings.Repeat("字", 2001),
			"说明正文不能超过 2000 个字符"},
		{"file 8001 runes", strings.Repeat("字", 8001),
			"技能文件不能超过 8000 个字符"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSkill(tc.file)
			if err == nil {
				t.Fatal("ParseSkill succeeded, want a readable error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestParseSkillErrorNamesPlace guards the readability contract: every parse
// error names either the line number or the offending field.
func TestParseSkillErrorNamesPlace(t *testing.T) {
	_, err := ParseSkill("名称：甲\n适用条件：x\n启用：是\n\n")
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "行") && !strings.Contains(err.Error(), "字段") {
		t.Errorf("error %q names neither a line nor a field", err)
	}
}
