package main

import (
	"encoding/json"
	"fmt"
)

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content []contentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

func (r toolResult) status() string {
	if r.IsError {
		return "error"
	}
	return "ok"
}

type tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Call        func(json.RawMessage) (toolResult, *rpcError)
}

var registry = []tool{
	{
		Name:        "formula_hint",
		Description: "给出数学或物理常见主题的公式与解题思路提示，只给提示、不给具体题目的答案。",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"topic": map[string]any{
					"type":        "string",
					"description": "数学或物理主题，如「勾股定理」「欧姆定律」",
				},
				"level": map[string]any{
					"type":        "string",
					"description": "提示详略，可选：简要、详细",
				},
			},
			"required": []string{"topic"},
		},
		Call: formulaHint,
	},
	{
		Name:        "unit_convert",
		Description: "在长度、质量、时间之间换算常用单位；跨量纲或未知单位会返回错误。",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "number", "description": "待换算的数值"},
				"from":  map[string]any{"type": "string", "description": "原单位，如 km"},
				"to":    map[string]any{"type": "string", "description": "目标单位，如 m"},
			},
			"required": []string{"value", "from", "to"},
		},
		Call: unitConvert,
	},
}

func lookupTool(name string) *tool {
	for i := range registry {
		if registry[i].Name == name {
			return &registry[i]
		}
	}
	return nil
}

func toolList() []map[string]any {
	list := make([]map[string]any, 0, len(registry))
	for _, t := range registry {
		list = append(list, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		})
	}
	return list
}

func textResult(text string) toolResult {
	return toolResult{Content: []contentItem{{Type: "text", Text: text}}}
}

func errorResult(format string, args ...any) toolResult {
	return toolResult{
		Content: []contentItem{{Type: "text", Text: fmt.Sprintf(format, args...)}},
		IsError: true,
	}
}

func decodeObject(raw json.RawMessage) (map[string]json.RawMessage, *rpcError) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, invalidParams("参数必须是 JSON 对象")
	}
	return obj, nil
}

func stringArg(obj map[string]json.RawMessage, name string, required bool) (string, *rpcError) {
	raw, ok := obj[name]
	if !ok || string(raw) == "null" {
		if required {
			return "", invalidParams("缺少必填参数 " + name)
		}
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", invalidParams("参数 " + name + " 必须是字符串")
	}
	return value, nil
}

func numberArg(obj map[string]json.RawMessage, name string, required bool) (float64, *rpcError) {
	raw, ok := obj[name]
	if !ok || string(raw) == "null" {
		if required {
			return 0, invalidParams("缺少必填参数 " + name)
		}
		return 0, nil
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, invalidParams("参数 " + name + " 必须是数字")
	}
	return value, nil
}
