package tutor

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/gateway"
	"campusclaw/backend/internal/mcp"
)

// cancellationProbe is a ToolChatter that cancels the request context after
// its first reply, which is when a client disconnect lands in production
// (after the remote call, before meta).
type cancellationProbe struct {
	cancel context.CancelFunc
	calls  []gateway.ToolCall
	rounds int
}

func (c *cancellationProbe) ChatWithTools(ctx context.Context, messages []gateway.ChatMessage, tools []gateway.ToolDefinition) (gateway.AssistantMessage, error) {
	c.rounds++
	if c.rounds > 1 {
		return gateway.AssistantMessage{Content: "不需要再调用工具"}, nil
	}
	c.cancel()
	return gateway.AssistantMessage{ToolCalls: c.calls}, nil
}

// TestAuditSurvivesRequestCancellation is the unit half of spec
// 「调用审计」Scenario 客户端断开后审计仍落库: the insert sees a live context
// even though the request's own context is already cancelled.
func TestAuditSurvivesRequestCancellation(t *testing.T) {
	base := context.WithValue(context.Background(), requestValueKey{}, "kept")
	ctx, cancel := context.WithCancel(base)
	defer cancel()

	var inserted []db.MCPToolCallInput
	h := NewHandlers(nil, nil, nil, newTestStage(defaultCatalog(), &fakeMCP{result: mcp.CallResult{Text: "提示"}}, &cancellationProbe{
		cancel: cancel,
		calls:  []gateway.ToolCall{toolCall("c1", "formula_hint__campus-tools", `{"topic":"勾股定理"}`)},
	}))
	h.insertToolCalls = func(writeCtx context.Context, _ *sql.DB, calls []db.MCPToolCallInput) error {
		if err := writeCtx.Err(); err != nil {
			t.Errorf("audit context is already dead (%v); the write would be lost", err)
		}
		if _, ok := writeCtx.Deadline(); !ok {
			t.Error("audit context has no deadline; a stalled database could hold the handler forever")
		}
		if got := writeCtx.Value(requestValueKey{}); got != "kept" {
			t.Errorf("audit context value = %v, want the request's values kept", got)
		}
		if ctx.Err() == nil {
			t.Error("the request context was not cancelled by the probe; the case proves nothing")
		}
		inserted = calls
		return nil
	}

	r := httptest.NewRequest(http.MethodPost, "/api/conversations/1/stream", nil).WithContext(ctx)
	meta := streamMeta{Status: "ok"}
	h.runToolStage(r, &db.SessionUser{UserID: 9, ClassID: 1}, &db.Conversation{ID: 4},
		&db.Skill{ID: 2, ClassID: 1, ToolsJSON: mustJSON(t, []string{"formula_hint@campus-tools"})},
		nil, &meta)

	if len(inserted) != 1 {
		t.Fatalf("audit rows = %+v, want one", inserted)
	}
	row := inserted[0]
	if row.Tool != "formula_hint" || row.Server != "campus-tools" || row.Status != toolCallStatusOK {
		t.Errorf("audit row = %+v", row)
	}
	if row.ClassID != 1 || row.UserID != 9 || row.ConversationID != 4 || row.Round != 1 {
		t.Errorf("audit row scope = %+v", row)
	}
	if len(meta.ToolCalls) != 1 || meta.ToolCalls[0].Tool != "formula_hint@campus-tools" || meta.ToolCalls[0].Status != "ok" {
		t.Errorf("meta.tool_calls = %+v", meta.ToolCalls)
	}
}

// requestValueKey stands in for the values the middleware puts on a request
// context (session user), which WithoutCancel must keep.
type requestValueKey struct{}

func TestAuditDetachesFromCancelButKeepsValues(t *testing.T) {
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), requestValueKey{}, "kept"))
	cancel()

	detached, cancelDetached := context.WithTimeout(context.WithoutCancel(ctx), auditTimeout)
	defer cancelDetached()

	if err := detached.Err(); err != nil {
		t.Errorf("detached context err = %v, want nil after the parent was cancelled", err)
	}
	if got := detached.Value(requestValueKey{}); got != "kept" {
		t.Errorf("value = %v, want kept", got)
	}
	deadline, ok := detached.Deadline()
	if !ok {
		t.Fatal("no deadline on the detached context")
	}
	if left := time.Until(deadline); left <= 0 || left > auditTimeout {
		t.Errorf("deadline in %v, want within %v", left, auditTimeout)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// TestAuditInputTruncatesOverlongNames is spec 「调用审计」Scenario
// 超长函数名不影响同轮审计 at the projection step: the name is cut to the
// column width so the row itself cannot fail the batch.
func TestAuditInputTruncatesOverlongNames(t *testing.T) {
	longTool := strings.Repeat("a", 100)
	longServer := strings.Repeat("b", 40)
	r := ToolCallRecord{
		Tool: longTool + "@" + longServer, ToolName: longTool, ServerName: longServer,
		ServerID: 7, Status: toolCallStatusDenied, Reason: reasonNotAllowed, Round: 1,
	}

	in := r.auditInput(1, 9, 4)
	if got := utf8.RuneCountInString(in.Tool); got != auditToolRunes {
		t.Errorf("tool length = %d, want %d", got, auditToolRunes)
	}
	if got := utf8.RuneCountInString(in.Server); got != auditServerRunes {
		t.Errorf("server length = %d, want %d", got, auditServerRunes)
	}
	if in.Status != toolCallStatusDenied || in.Reason != reasonNotAllowed || in.Round != 1 {
		t.Errorf("other fields changed: %+v", in)
	}

	// meta keeps the original value: truncation is a storage concern only.
	if summary := r.summary(); !strings.HasSuffix(summary.Tool, longTool+"@"+longServer) {
		t.Errorf("summary tool = %q, want the untruncated value", summary.Tool)
	}

	short := ToolCallRecord{ToolName: "formula_hint", ServerName: "campus-tools", Status: toolCallStatusOK}
	in = short.auditInput(1, 9, 4)
	if in.Tool != "formula_hint" || in.Server != "campus-tools" {
		t.Errorf("short names must pass through unchanged: %+v", in)
	}
}
