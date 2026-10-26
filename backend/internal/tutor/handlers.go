package tutor

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
)

// Body limits are generous enough for the longest legal payload (a
// 8000-character skill file is at most 24 KiB of UTF-8) and small enough that
// an oversized body is rejected before it is parsed.
const (
	maxAgentBodyBytes        = 16 << 10
	maxSkillBodyBytes        = 32 << 10
	maxConversationBodyBytes = 8 << 10
	maxAgentNameRunes        = 64
	maxAgentPromptRunes      = 2000
)

// Handlers is the HTTP surface of the tutor domain (agents, skills,
// conversations). Every handler trusts only the session attached to the
// request context for role and class; query, header and body parameters
// claiming a class, a role or a user are ignored (design.md Decision 4).
type Handlers struct {
	Conn *sql.DB
	// Retriever and Chat serve the streaming ask endpoint only.
	Retriever MaterialRetriever
	Chat      StreamChatter
	// Tools runs the MCP tool stage. Nil means no tool support (tests that do
	// not exercise it), and the turn behaves exactly like iteration 3.
	Tools *ToolStage

	// locks serialises answer generation per conversation: a question asked
	// while the previous answer is still being generated answers 409
	// (design.md Decision 5). Entries live for the process lifetime; their
	// number is bounded by the conversations touched.
	locks sync.Map
}

func NewHandlers(conn *sql.DB, retriever MaterialRetriever, chat StreamChatter, tools *ToolStage) *Handlers {
	return &Handlers{Conn: conn, Retriever: retriever, Chat: chat, Tools: tools}
}

// lockFor returns the generation lock of one conversation, creating it on
// first use.
func (h *Handlers) lockFor(conversationID int) *sync.Mutex {
	m, _ := h.locks.LoadOrStore(conversationID, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// readBody reads a bounded request body; on failure it writes 400 itself.
func readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "请求格式错误")
		return nil, false
	}
	return body, true
}

// pathID parses the {id} path segment. A non-numeric id is answered exactly
// like a missing row: callers cannot tell the two apart (design.md Decision 4).
func pathID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, httpapi.MsgNotFound)
		return 0, false
	}
	return id, true
}

// writeWriteError maps a persistence error to the agent/skill write responses:
// a rejected binding is the same 404 as a missing object, a name clash is a
// readable 400, anything else a 503 that names no dependency.
func writeWriteError(w http.ResponseWriter, err error, duplicateMessage string) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, httpapi.MsgNotFound)
	case errors.Is(err, db.ErrDuplicateName):
		httpapi.WriteError(w, http.StatusBadRequest, duplicateMessage)
	default:
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
	}
}

// nonNilIDs turns a nil slice into an empty one so bindings marshal as [].
func nonNilIDs(ids []int) []int {
	if ids == nil {
		return []int{}
	}
	return ids
}

// agentBody is the accepted body of POST /api/agents and PUT /api/agents/{id}.
// Unknown fields — class_id, role, ... — are dropped by the decoder, and an
// omitted binding array is an empty one (whole-replacement semantics).
type agentBody struct {
	Name        string `json:"name"`
	Prompt      string `json:"prompt"`
	Enabled     bool   `json:"enabled"`
	SkillIDs    []int  `json:"skill_ids"`
	MaterialIDs []int  `json:"material_ids"`
} // @name AgentRequest

// toAgent validates the body and scopes it to the session's class. The name is
// stored trimmed; the prompt is stored verbatim (empty is legal).
func (b agentBody) toAgent(classID int) (db.TutorAgent, error) {
	name := strings.TrimSpace(b.Name)
	switch {
	case name == "":
		return db.TutorAgent{}, errors.New("名称不能为空")
	case utf8.RuneCountInString(name) > maxAgentNameRunes:
		return db.TutorAgent{}, fmt.Errorf("名称不能超过 %d 个字符", maxAgentNameRunes)
	case utf8.RuneCountInString(b.Prompt) > maxAgentPromptRunes:
		return db.TutorAgent{}, fmt.Errorf("提示词不能超过 %d 个字符", maxAgentPromptRunes)
	}
	return db.TutorAgent{
		ClassID:     classID,
		Name:        name,
		Prompt:      b.Prompt,
		Enabled:     b.Enabled,
		SkillIDs:    b.SkillIDs,
		MaterialIDs: b.MaterialIDs,
	}, nil
}

// ListAgents implements GET /api/agents. A teacher sees every agent of the
// class with prompt, enabled flag and binding ids; a student sees only the
// enabled agents and only their id and name (spec: 学生只见已启用).
//
// @Summary 智能体列表
// @Description 教师看到本班全部智能体（含提示词、启用状态与绑定编号）；学生只看到已启用的智能体，且响应中只有 id 与 name。
// @Tags 智能体
// @Produce json
// @Success 200 {array} map[string]interface{} "教师：id、name、prompt、enabled、skill_ids、material_ids；学生：id、name"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/agents [get]
func (h *Handlers) ListAgents(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())
	teacher := su.Role == db.RoleTeacher

	agents, err := db.ListAgents(r.Context(), h.Conn, su.ClassID, !teacher)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	if !teacher {
		type item struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}
		out := make([]item, 0, len(agents))
		for _, a := range agents {
			out = append(out, item{ID: a.ID, Name: a.Name})
		}
		httpapi.WriteJSON(w, http.StatusOK, out)
		return
	}

	type item struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		Prompt      string `json:"prompt"`
		Enabled     bool   `json:"enabled"`
		SkillIDs    []int  `json:"skill_ids"`
		MaterialIDs []int  `json:"material_ids"`
	}
	out := make([]item, 0, len(agents))
	for _, a := range agents {
		out = append(out, item{
			ID: a.ID, Name: a.Name, Prompt: a.Prompt, Enabled: a.Enabled,
			SkillIDs: nonNilIDs(a.SkillIDs), MaterialIDs: nonNilIDs(a.MaterialIDs),
		})
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

// CreateAgent implements POST /api/agents (teacher only, enforced upstream).
//
// @Summary 新建智能体
// @Description 仅教师可用。skill_ids / material_ids 必须属于本班，否则 404；名称重复 400。省去的绑定数组视为空数组。
// @Tags 智能体
// @Accept json
// @Produce json
// @Param request body agentBody true "智能体定义"
// @Success 201 {object} map[string]int "新智能体的 id"
// @Failure 400 {object} httpapi.errorBody "名称或提示词不合法、名称重复"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 403 {object} httpapi.errorBody "学生无权操作"
// @Failure 404 {object} httpapi.errorBody "绑定的技能或材料不存在（含他班）"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/agents [post]
func (h *Handlers) CreateAgent(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())

	body, ok := readBody(w, r, maxAgentBodyBytes)
	if !ok {
		return
	}
	var in agentBody
	if err := json.Unmarshal(body, &in); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	a, err := in.toAgent(su.ClassID)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	id, err := db.CreateAgent(r.Context(), h.Conn, a)
	if err != nil {
		writeWriteError(w, err, "已存在同名智能体")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, map[string]int{"id": id})
}

// UpdateAgent implements PUT /api/agents/{id} (teacher only): a whole
// replacement. The path id is validated before the body is read, so a
// cross-class id and a nonexistent id answer with the same 404 whichever body
// was submitted.
//
// @Summary 更新智能体
// @Description 仅教师可用，整体替换。他班与不存在的编号是同一个 404（先于读取请求体判断）。
// @Tags 智能体
// @Accept json
// @Produce json
// @Param id path int true "智能体编号"
// @Param request body agentBody true "智能体定义"
// @Success 200 {object} map[string]int "被更新的 id"
// @Failure 400 {object} httpapi.errorBody "名称或提示词不合法、名称重复"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 403 {object} httpapi.errorBody "学生无权操作"
// @Failure 404 {object} httpapi.errorBody "智能体或绑定对象不存在（含他班）"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/agents/{id} [put]
func (h *Handlers) UpdateAgent(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())

	id, ok := pathID(w, r)
	if !ok {
		return
	}
	body, ok := readBody(w, r, maxAgentBodyBytes)
	if !ok {
		return
	}
	var in agentBody
	if err := json.Unmarshal(body, &in); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	a, err := in.toAgent(su.ClassID)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.ID = id

	if err := db.UpdateAgent(r.Context(), h.Conn, a); err != nil {
		writeWriteError(w, err, "已存在同名智能体")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]int{"id": id})
}
