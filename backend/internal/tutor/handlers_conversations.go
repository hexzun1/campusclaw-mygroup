package tutor

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
)

// conversationItem is one conversation in every response that carries a
// conversation; DetailConversation adds the ordered messages around it.
type conversationItem struct {
	ID        int       `json:"id"`
	AgentID   int       `json:"agent_id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
} // @name Conversation

func toConversationItem(c db.Conversation) conversationItem {
	return conversationItem{
		ID: c.ID, AgentID: c.AgentID, Title: c.Title,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

// ListConversations implements GET /api/conversations: only the caller's own
// conversations of the caller's class, most recently updated first.
//
// @Summary 会话列表
// @Description 只返回调用者本人在本班的会话，最近更新的在前。
// @Tags 会话
// @Produce json
// @Success 200 {array} conversationItem "id、agent_id、title、created_at、updated_at"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/conversations [get]
func (h *Handlers) ListConversations(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())

	convs, err := db.ListConversations(r.Context(), h.Conn, su.ClassID, su.UserID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	out := make([]conversationItem, 0, len(convs))
	for _, c := range convs {
		out = append(out, toConversationItem(c))
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

// createConversationBody names one agent; any other field is dropped.
type createConversationBody struct {
	AgentID int `json:"agent_id"`
} // @name CreateConversationRequest

// CreateConversation implements POST /api/conversations.
//
// The judgement order is the design's (design.md Decision 4): a class with no
// enabled agent answers 400 — nothing the caller specified is wrong — while a
// specified agent that is disabled, in another class or missing answers the
// same 404 as any nonexistent object. The new conversation locks that agent
// for its whole life.
//
// @Summary 新建会话
// @Description 指定一个本班已启用的智能体，会话从此锁定它。本班没有已启用智能体时 400；智能体停用、他班或不存在时为同一个 404。
// @Tags 会话
// @Accept json
// @Produce json
// @Param request body createConversationBody true "agent_id"
// @Success 201 {object} conversationItem "id、agent_id、title、created_at、updated_at"
// @Failure 400 {object} httpapi.errorBody "缺少 agent_id，或本班暂无已启用的智能体"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 404 {object} httpapi.errorBody "智能体不存在（含他班、停用）"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/conversations [post]
func (h *Handlers) CreateConversation(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())

	body, ok := readBody(w, r, maxConversationBodyBytes)
	if !ok {
		return
	}
	var in createConversationBody
	if err := json.Unmarshal(body, &in); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if in.AgentID <= 0 {
		httpapi.WriteError(w, http.StatusBadRequest, "缺少智能体编号")
		return
	}

	n, err := db.CountEnabledAgents(r.Context(), h.Conn, su.ClassID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}
	if n == 0 {
		httpapi.WriteError(w, http.StatusBadRequest, "本班暂无已启用的助手")
		return
	}

	// enabledOnly: a disabled or foreign agent is indistinguishable from a
	// nonexistent one, and the conversation must never start against it.
	if _, err := db.GetAgent(r.Context(), h.Conn, in.AgentID, su.ClassID, true); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			httpapi.WriteError(w, http.StatusNotFound, httpapi.MsgNotFound)
		} else {
			httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		}
		return
	}

	id, err := db.CreateConversation(r.Context(), h.Conn, su.ClassID, su.UserID, in.AgentID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}
	conv, err := db.GetConversation(r.Context(), h.Conn, id, su.ClassID, su.UserID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, toConversationItem(*conv))
}

// messageItem is one persisted turn. Citations are passed through exactly as
// the orchestrator wrote them; a message without citations answers [].
type messageItem struct {
	ID        int             `json:"id"`
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Citations json.RawMessage `json:"citations"`
	SkillName string          `json:"skill_name"`
	CreatedAt time.Time       `json:"created_at"`
} // @name TutorMessage

// conversationDetail is the body of GET /api/conversations/{id}: the
// conversation plus its messages in order.
type conversationDetail struct {
	conversationItem
	Messages []messageItem `json:"messages"`
} // @name ConversationDetail

// DetailConversation implements GET /api/conversations/{id}: the conversation
// with its messages in order. The database read carries class and user, so
// another user's, another class's and a nonexistent conversation all answer
// with the byte-identical 404 (spec: 他人与他班会话一致 404).
//
// @Summary 会话详情
// @Description 返回会话与按时间排序的全部消息；assistant 消息带 citations（与正文中的 [n] 对应）与 skill_name。他人、他班或不存在的会话都是同一个 404。
// @Tags 会话
// @Produce json
// @Param id path int true "会话编号"
// @Success 200 {object} conversationDetail "会话字段 + messages"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 404 {object} httpapi.errorBody "会话不存在（含他人、他班）"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/conversations/{id} [get]
func (h *Handlers) DetailConversation(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())

	id, ok := pathID(w, r)
	if !ok {
		return
	}
	conv, err := db.GetConversation(r.Context(), h.Conn, id, su.ClassID, su.UserID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			httpapi.WriteError(w, http.StatusNotFound, httpapi.MsgNotFound)
		} else {
			httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		}
		return
	}

	msgs, err := db.ListMessages(r.Context(), h.Conn, conv.ID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	out := conversationDetail{conversationItem: toConversationItem(*conv), Messages: make([]messageItem, 0, len(msgs))}
	for _, m := range msgs {
		citations := json.RawMessage(m.CitationsJSON)
		if len(citations) == 0 {
			citations = json.RawMessage("[]")
		}
		out.Messages = append(out.Messages, messageItem{
			ID: m.ID, Role: m.Role, Content: m.Content,
			Citations: citations, SkillName: m.SkillName, CreatedAt: m.CreatedAt,
		})
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}
