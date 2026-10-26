package tutor

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStreamMetaToolCallsOmitEmpty(t *testing.T) {
	raw, err := json.Marshal(streamMeta{Status: "ok", Citations: []Source{}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "tool_calls") {
		t.Errorf("meta without calls must omit tool_calls: %s", raw)
	}
}

func TestStreamMetaToolCallsCarryNoArgumentsOrResults(t *testing.T) {
	raw, err := json.Marshal(streamMeta{
		Status: "ok", Citations: []Source{},
		ToolCalls: []ToolCallSummary{{
			Tool: "formula_hint@campus-tools", Status: "ok", Round: 1, DurationMS: 12,
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"tool_calls":[{"tool":"formula_hint@campus-tools","status":"ok","round":1,"duration_ms":12}]`) {
		t.Errorf("unexpected tool_calls shape: %s", text)
	}
	for _, forbidden := range []string{"arguments", "arguments_text", "result", "token"} {
		if strings.Contains(text, `"`+forbidden+`"`) {
			t.Errorf("meta leaks %q: %s", forbidden, text)
		}
	}
}
