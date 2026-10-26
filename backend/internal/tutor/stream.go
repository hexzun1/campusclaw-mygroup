package tutor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/gateway"
	"campusclaw/backend/internal/httpapi"
	"campusclaw/backend/internal/search"
)

const (
	// Question bounds of the streaming endpoint, counted in runes
	// (spec: 流式响应协议).
	minQuestionRunes = 2
	maxQuestionRunes = 500
	// streamTopK is how many chunks one turn may cite.
	streamTopK = 4
)

// msgStreamAborted is the generic mid-stream failure text: it names no
// internal detail (spec: 流式响应协议).
const msgStreamAborted = "回答生成中断，请重试"

// MaterialRetriever is the slice of internal/search the streaming pipeline
// needs: hybrid retrieval restricted to a bound subset of the class's
// materials.
type MaterialRetriever interface {
	HybridInMaterials(ctx context.Context, classID int, materialIDs []int, query string, topK int) ([]search.Hit, error)
}

// StreamChatter is the slice of the chat gateway the streaming pipeline needs.
type StreamChatter interface {
	StreamChat(ctx context.Context, messages []gateway.ChatMessage, onDelta func(string) error) error
}

// streamQuestionBody is the accepted body of the streaming endpoint. Unknown
// fields — agent_id, class_id, ... — are dropped by the decoder: the turn
// always uses the conversation's locked agent (spec: 会话锁定智能体).
type streamQuestionBody struct {
	Question string `json:"question"`
} // @name StreamQuestionRequest

// StreamConversation implements POST /api/conversations/{id}/stream. It runs
// the 11-step pipeline of design.md Decision 5 and answers with SSE
// (design.md Decision 8).
//
// The 200 and the `meta` event are withheld until the first pushable text
// leaves the output filter; everything that can fail before that — session,
// membership (404), concurrency (409), question shape (400), database reads,
// retrieval and the gateway before its first delta (503) — is a plain JSON
// error. After the stream has started, a failure or a client disconnect
// persists what was pushed and, while the client is still there, appends an
// `error` event.
//
// @Summary 流式提问
// @Description 以 SSE 返回一次作答。事件序列：meta（status、skill、citations，本轮涉及 MCP 工具时另有 tool_calls）→ delta…（正文增量，拼接后即整段正文）→ done（message_id）；中途失败时在已推送部分之后给出 error。第一段正文之前的一切失败都是普通 JSON 错误。同一会话同时只允许一次生成，并发提问得到 409。响应头为 text/event-stream、Cache-Control: no-cache。
// @Tags 会话
// @Accept json
// @Produce text/event-stream
// @Param id path int true "会话编号"
// @Param request body streamQuestionBody true "问题（2–500 字）"
// @Success 200 {string} string "SSE 事件流：meta / delta / done / error"
// @Failure 400 {object} httpapi.errorBody "问题为空、过短或过长"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 404 {object} httpapi.errorBody "会话不存在（含他人、他班）"
// @Failure 409 {object} httpapi.errorBody "回答正在生成，请稍后重试"
// @Failure 503 {object} httpapi.errorBody "检索或对话依赖不可用；生成中断且未产生任何正文时也是 503"
// @Security BearerAuth
// @Router /api/conversations/{id}/stream [post]
func (h *Handlers) StreamConversation(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	body, ok := readBody(w, r, maxConversationBodyBytes)
	if !ok {
		return
	}
	var in streamQuestionBody
	if err := json.Unmarshal(body, &in); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	question := strings.TrimSpace(in.Question)
	if n := utf8.RuneCountInString(question); n < minQuestionRunes || n > maxQuestionRunes {
		httpapi.WriteError(w, http.StatusBadRequest, fmt.Sprintf("问题需为 %d 到 %d 个字符", minQuestionRunes, maxQuestionRunes))
		return
	}

	// Step 1: the conversation must be the caller's own, in the caller's
	// class; the lock serialises generation inside it.
	conv, err := db.GetConversation(r.Context(), h.Conn, id, su.ClassID, su.UserID)
	if err != nil {
		writeStreamLookupError(w, err)
		return
	}

	lock := h.lockFor(conv.ID)
	if !lock.TryLock() {
		httpapi.WriteError(w, http.StatusConflict, "回答正在生成，请稍后重试")
		return
	}
	defer lock.Unlock()

	// Step 2: the locked agent; a disabled agent still answers its existing
	// conversations (spec: 已停用智能体的已有会话仍可提问).
	agent, err := db.GetAgent(r.Context(), h.Conn, conv.AgentID, su.ClassID, false)
	if err != nil {
		writeStreamLookupError(w, err)
		return
	}

	// Step 3: candidate skills re-read on every question, so a changed
	// binding or enabled flag takes effect immediately.
	skills, err := db.ListAgentSkillsEnabled(r.Context(), h.Conn, agent.ID, su.ClassID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	// Step 4: at most one skill routes the turn.
	skill := Route(question, skills)

	// Step 5: persist the question before any retrieval, so even a later
	// 503 leaves it in the conversation.
	if err := db.AppendQuestion(r.Context(), h.Conn, conv.ID, question); err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	// Step 6: retrieval runs only inside the bound materials, and not at all
	// without bindings.
	var hits []search.Hit
	if len(agent.MaterialIDs) > 0 {
		hits, err = h.Retriever.HybridInMaterials(r.Context(), su.ClassID, agent.MaterialIDs, question, streamTopK)
		if err != nil {
			httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
			return
		}
	}

	// Step 7: drop empty bodies, apply the skill's 资料 scope, renumber.
	subject := ""
	if skill != nil {
		subject = skill.SubjectText
	}
	sources := BuildCitations(hits, subject)
	citationsJSON, err := json.Marshal(sources)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	// Step 8: the meta event is prepared but not written yet.
	var skillName *string
	skillNameStr := ""
	if skill != nil {
		skillName = &skill.Name
		skillNameStr = skill.Name
	}
	meta := streamMeta{Status: "ok", Skill: skillName, Citations: sources}

	// Step 9: assemble the base gateway request from the persisted window.
	history, err := db.ListRecentMessages(r.Context(), h.Conn, conv.ID, HistoryLimit)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}
	messageInput := MessageInput{
		Prompt:   agent.Prompt,
		Skill:    skill,
		Sources:  sources,
		History:  history,
		Question: question,
	}
	messages := BuildMessages(messageInput)

	// Step 9b: the MCP tool stage runs before meta exists, and its audit rows
	// are persisted before the first byte of the stream (design.md Decision 8).
	toolResults, stageRan := h.runToolStage(r, su, conv, skill, messages, &meta)

	// Step 9c: the final request carries successful tool results in the user
	// message and the ⑥ declaration in the system message, never tool
	// definitions.
	if stageRan || len(toolResults) > 0 {
		messageInput.ToolResults = toolResults
		messageInput.ToolStage = stageRan
		messages = BuildMessages(messageInput)
	}

	// Step 10: stream the answer through the output filter. Every push is a
	// delta; their concatenation is exactly what step 11 persists.
	out := sseStream{w: w, c: http.NewResponseController(w)}
	filter := NewOutputFilter(len(sources))
	var pushed strings.Builder
	started := false

	push := func(text string) error {
		if text == "" {
			return nil
		}
		if !started {
			started = true
			out.start()
			if err := out.event("meta", meta); err != nil {
				return err
			}
		}
		if err := out.event("delta", deltaEvent{Text: text}); err != nil {
			return err
		}
		pushed.WriteString(text)
		return nil
	}

	streamErr := h.Chat.StreamChat(r.Context(), messages, func(piece string) error {
		return push(filter.Feed(piece))
	})

	remaining, banned := filter.Finish()
	if banned || !started {
		// No pushable text ever left the filter: either the whole output was
		// the banned answer or the generation failed while everything was
		// still withheld. Both are pre-first-text failures — a plain JSON 503
		// and no assistant message.
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}
	if err := push(remaining); err != nil {
		// The client went away just now; the pushed text is still persisted.
		streamErr = err
	}

	// Step 11: persist the pushed text under a context that survives the
	// client disconnect, then close the stream.
	persistCtx := context.WithoutCancel(r.Context())
	messageID, err := db.InsertAssistantMessage(persistCtx, h.Conn, conv.ID, pushed.String(), citationsJSON, skillNameStr)
	if err != nil {
		writeStreamEnd(w, r, out, 0, true)
		return
	}
	writeStreamEnd(w, r, out, messageID, streamErr != nil)
}

// runToolStage resolves the routed skill's MCP references, runs the tool
// loop, writes the audit rows and fills meta.tool_calls. Every failure is
// degraded to "answer without tools": the turn never stops because of it.
func (h *Handlers) runToolStage(r *http.Request, su *db.SessionUser, conv *db.Conversation, skill *db.Skill, messages []gateway.ChatMessage, meta *streamMeta) ([]ToolResult, bool) {
	if h.Tools == nil || skill == nil {
		return nil, false
	}
	var refs []string
	for _, ref := range unmarshalTools(skill.ToolsJSON) {
		if _, _, ok := splitMCPToolRef(ref); ok {
			refs = append(refs, ref)
		}
	}
	if len(refs) == 0 {
		return nil, false
	}

	candidates, denied, err := h.Tools.Resolve(r.Context(), su.ClassID, refs)
	if err != nil {
		log.Printf("tutor: resolve mcp tool candidates: %v", err)
		return nil, false
	}
	records := denied
	var results []ToolResult
	ran := false
	if len(candidates) > 0 {
		outcome := h.Tools.Run(r.Context(), ToolStageInput{
			ClassID:    su.ClassID,
			Messages:   messages,
			Candidates: candidates,
		})
		records = append(records, outcome.Records...)
		results = outcome.Results
		ran = outcome.Ran
	}
	if len(records) == 0 {
		return results, ran
	}

	inputs := make([]db.MCPToolCallInput, 0, len(records))
	summaries := make([]ToolCallSummary, 0, len(records))
	for _, record := range records {
		inputs = append(inputs, record.auditInput(su.ClassID, su.UserID, conv.ID))
		summaries = append(summaries, record.summary())
	}
	// The audit must survive even if the answer fails later, so it is
	// committed before meta is written; a failure here is logged and the
	// answer still proceeds.
	if err := db.InsertToolCalls(r.Context(), h.Conn, inputs); err != nil {
		log.Printf("tutor: insert mcp tool call audit: %v", err)
	}
	meta.ToolCalls = summaries
	return results, ran
}

// writeStreamEnd appends the terminal event: `done` with the persisted
// message id, or `error` after a mid-stream failure. A client that is already
// gone gets neither.
func writeStreamEnd(w http.ResponseWriter, r *http.Request, out sseStream, messageID int, failed bool) {
	if r.Context().Err() != nil {
		return
	}
	if failed {
		_ = out.event("error", errorEvent{Message: msgStreamAborted})
		return
	}
	_ = out.event("done", doneEvent{MessageID: messageID})
}

// writeStreamLookupError maps a lookup failure to its response: a missing,
// foreign or other-class row is the byte-identical 404, anything else the
// usual 503 (spec: 他人与他班会话一致 404).
func writeStreamLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, httpapi.MsgNotFound)
		return
	}
	httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
}

// streamMeta is the first event: the turn's status, the routed skill's name
// (null when none matched) and the numbered citations.
type streamMeta struct {
	Status    string            `json:"status"`
	Skill     *string           `json:"skill"`
	Citations []Source          `json:"citations"`
	ToolCalls []ToolCallSummary `json:"tool_calls,omitempty"`
}

// deltaEvent carries one increment of the answer; concatenated deltas equal
// the persisted assistant message body.
type deltaEvent struct {
	Text string `json:"text"`
}

// doneEvent closes the stream with the persisted message's id.
type doneEvent struct {
	MessageID int `json:"message_id"`
}

// errorEvent reports a mid-stream failure with a generic, detail-free text.
type errorEvent struct {
	Message string `json:"message"`
}

// sseStream writes SSE frames and flushes each one immediately. Flushing
// through http.ResponseController reaches the real writer even when the
// ResponseWriter is wrapped.
type sseStream struct {
	w http.ResponseWriter
	c *http.ResponseController
}

// start writes the event-stream headers and the 200, overriding the no-store
// the outer middleware preset (design.md Decision 8).
func (s sseStream) start() {
	h := s.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	s.w.WriteHeader(http.StatusOK)
}

// event writes one frame — `event: <name>\ndata: <single-line JSON>\n\n` —
// and flushes it.
func (s sseStream) event(name string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, encoded); err != nil {
		return err
	}
	return s.c.Flush()
}
