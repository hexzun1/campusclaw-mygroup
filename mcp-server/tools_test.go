package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func callToolForTest(t *testing.T, name, args string) toolResult {
	t.Helper()
	raw, err := json.Marshal(callParams{Name: name, Arguments: json.RawMessage(args)})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	result, rpcErr := (&server{token: testToken}).callTool(raw)
	if rpcErr != nil {
		t.Fatalf("callTool(%s) failed: %d %s", name, rpcErr.Code, rpcErr.Message)
	}
	return result
}

func TestToolsListNamesAndSchemas(t *testing.T) {
	tools := toolList()
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if !toolNamePattern.MatchString(name) {
			t.Errorf("tool name %q does not match %s", name, toolNamePattern)
		}
		schema, _ := tool["inputSchema"].(map[string]any)
		if schema["type"] != "object" {
			t.Errorf("%s inputSchema.type = %v, want object", name, schema["type"])
		}
	}
}

func TestToolsFormulaHintTopicKeyword(t *testing.T) {
	result := callToolForTest(t, "formula_hint", `{"topic":"勾股定理"}`)
	text := result.Content[0].Text
	if result.IsError {
		t.Fatalf("isError = true: %s", text)
	}
	if !strings.Contains(text, "a² + b² = c²") {
		t.Fatalf("text %q does not contain the theorem formula", text)
	}
	if !strings.Contains(text, "思路") {
		t.Fatalf("text %q does not contain the usage idea", text)
	}
}

func TestToolsFormulaHintMatchesFullSentence(t *testing.T) {
	result := callToolForTest(t, "formula_hint", `{"topic":"勾股定理怎么求斜边","level":"详细"}`)
	text := result.Content[0].Text
	if result.IsError {
		t.Fatalf("isError = true: %s", text)
	}
	if !strings.Contains(text, "勾股定理") || !strings.Contains(text, "a² + b² = c²") {
		t.Fatalf("text %q does not match the keyword inside a sentence", text)
	}
	if !strings.Contains(text, "提示：") {
		t.Fatalf("detailed level did not add the extra hint: %q", text)
	}
}

func TestToolsFormulaHintUnknownTopic(t *testing.T) {
	result := callToolForTest(t, "formula_hint", `{"topic":"量子色动力学"}`)
	if result.IsError {
		t.Fatal("isError = true, want false for an unknown topic")
	}
	if got := strings.TrimSpace(result.Content[0].Text); got != "暂无该主题的提示" {
		t.Fatalf("text = %q, want 暂无该主题的提示", got)
	}
}

func TestToolsFormulaHintMissingTopic(t *testing.T) {
	raw := json.RawMessage(`{"name":"formula_hint","arguments":{}}`)
	if _, rpcErr := (&server{token: testToken}).callTool(raw); rpcErr == nil || rpcErr.Code != codeInvalidParams {
		t.Fatalf("rpcErr = %+v, want -32602", rpcErr)
	}
}

func TestToolsUnitConvert(t *testing.T) {
	result := callToolForTest(t, "unit_convert", `{"value":1.5,"from":"km","to":"m"}`)
	if result.IsError {
		t.Fatalf("isError = true: %s", result.Content[0].Text)
	}
	if got := result.Content[0].Text; !strings.Contains(got, "1500") {
		t.Fatalf("text = %q, want it to contain 1500", got)
	}
}

func TestToolsUnitConvertRejectsCrossDimension(t *testing.T) {
	result := callToolForTest(t, "unit_convert", `{"value":1,"from":"kg","to":"s"}`)
	if !result.IsError {
		t.Fatalf("isError = false, text = %q", result.Content[0].Text)
	}
	if got := result.Content[0].Text; !strings.Contains(got, "跨量纲") {
		t.Fatalf("text = %q, want a readable cross-dimension reason", got)
	}
}

func TestToolsUnitConvertRejectsUnknownUnit(t *testing.T) {
	result := callToolForTest(t, "unit_convert", `{"value":1,"from":"parsec","to":"m"}`)
	if !result.IsError {
		t.Fatal("isError = false for an unknown unit")
	}
	if !strings.Contains(result.Content[0].Text, "parsec") {
		t.Fatalf("text %q should name the unsupported unit", result.Content[0].Text)
	}
}
