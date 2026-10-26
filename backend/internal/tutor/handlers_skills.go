package tutor

import (
	"encoding/json"
	"errors"
	"net/http"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
)

// skillBody is the accepted body of the skill write endpoints: the raw skill
// file text. The fields are derived from it by ParseSkill; a request cannot
// set enabled, name or anything else directly (spec: 启用状态以文件为准).
type skillBody struct {
	Source string `json:"source"`
} // @name SkillRequest

// unmarshalTools renders the stored tools_json as a JSON array. Only
// ParseSkill ever writes this column, so it is always a valid array.
func unmarshalTools(raw []byte) []string {
	var tools []string
	if err := json.Unmarshal(raw, &tools); err != nil || tools == nil {
		return []string{}
	}
	return tools
}

// checkSkillMCPTools writes the failure response and returns false when a
// reference is invalid or the lookup failed; true means the skill may be saved.
func (h *Handlers) checkSkillMCPTools(w http.ResponseWriter, r *http.Request, classID int, toolsJSON []byte) bool {
	if err := validateSkillMCPTools(r.Context(), h.Conn, classID, unmarshalTools(toolsJSON)); err != nil {
		var invalid *invalidSkillToolError
		if errors.As(err, &invalid) {
			httpapi.WriteError(w, http.StatusBadRequest, invalid.Error())
		} else {
			httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		}
		return false
	}
	return true
}

// ListSkills implements GET /api/skills. A teacher sees every skill of the
// class including its source text; a student sees only enabled skills and
// never their source (spec: 学生看到的技能).
//
// @Summary 技能列表
// @Description 教师看到本班全部技能（含源文件文本）；学生只看到已启用的技能，且响应中没有 source_text。
// @Tags 技能
// @Produce json
// @Success 200 {array} map[string]interface{} "教师：id、name、trigger_text、subject_text、tools、description、source_text、enabled；学生：同左但不含 source_text"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/skills [get]
func (h *Handlers) ListSkills(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())
	teacher := su.Role == db.RoleTeacher

	skills, err := db.ListSkills(r.Context(), h.Conn, su.ClassID, !teacher)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	if !teacher {
		type item struct {
			ID          int      `json:"id"`
			Name        string   `json:"name"`
			TriggerText string   `json:"trigger_text"`
			SubjectText string   `json:"subject_text"`
			Tools       []string `json:"tools"`
			Description string   `json:"description"`
			Enabled     bool     `json:"enabled"`
		}
		out := make([]item, 0, len(skills))
		for _, s := range skills {
			out = append(out, item{
				ID: s.ID, Name: s.Name, TriggerText: s.TriggerText, SubjectText: s.SubjectText,
				Tools: unmarshalTools(s.ToolsJSON), Description: s.Description, Enabled: s.Enabled,
			})
		}
		httpapi.WriteJSON(w, http.StatusOK, out)
		return
	}

	type item struct {
		ID          int      `json:"id"`
		Name        string   `json:"name"`
		TriggerText string   `json:"trigger_text"`
		SubjectText string   `json:"subject_text"`
		Tools       []string `json:"tools"`
		Description string   `json:"description"`
		SourceText  string   `json:"source_text"`
		Enabled     bool     `json:"enabled"`
	}
	out := make([]item, 0, len(skills))
	for _, s := range skills {
		out = append(out, item{
			ID: s.ID, Name: s.Name, TriggerText: s.TriggerText, SubjectText: s.SubjectText,
			Tools: unmarshalTools(s.ToolsJSON), Description: s.Description,
			SourceText: s.SourceText, Enabled: s.Enabled,
		})
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

// CreateSkill implements POST /api/skills (teacher only). A file that does not
// parse answers 400 with the parser's own readable message — line number or
// field name included — and nothing is written.
//
// @Summary 新建技能
// @Description 仅教师可用。source 为技能文件全文：多行“字段：值”（名称、适用条件必填；资料、工具、启用可选），空行之后是说明正文。字段解析失败返回 400 并指出行号；启用状态以文件中的“启用”为准。
// @Tags 技能
// @Accept json
// @Produce json
// @Param request body skillBody true "技能文件（source 字段）"
// @Success 201 {object} map[string]int "新技能的 id"
// @Failure 400 {object} httpapi.errorBody "技能文件解析失败、名称重复"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 403 {object} httpapi.errorBody "学生无权操作"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/skills [post]
func (h *Handlers) CreateSkill(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())

	body, ok := readBody(w, r, maxSkillBodyBytes)
	if !ok {
		return
	}
	var in skillBody
	if err := json.Unmarshal(body, &in); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	s, err := ParseSkill(in.Source)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.checkSkillMCPTools(w, r, su.ClassID, s.ToolsJSON) {
		return
	}
	s.ClassID = su.ClassID

	id, err := db.CreateSkill(r.Context(), h.Conn, s)
	if err != nil {
		writeWriteError(w, err, "已存在同名技能")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, map[string]int{"id": id})
}

// UpdateSkill implements PUT /api/skills/{id} (teacher only). The existence
// check comes first, before the body is read, so a B-class skill and a
// nonexistent id answer with the same 404 whatever the submitted file says
// (spec: 他班对象一致 404).
//
// @Summary 更新技能
// @Description 仅教师可用，整体替换（文件重新解析）。他班与不存在的编号是同一个 404（先于读取请求体判断）。
// @Tags 技能
// @Accept json
// @Produce json
// @Param id path int true "技能编号"
// @Param request body skillBody true "技能文件（source 字段）"
// @Success 200 {object} map[string]int "被更新的 id"
// @Failure 400 {object} httpapi.errorBody "技能文件解析失败、名称重复"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 403 {object} httpapi.errorBody "学生无权操作"
// @Failure 404 {object} httpapi.errorBody "技能不存在（含他班）"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/skills/{id} [put]
func (h *Handlers) UpdateSkill(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())

	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := db.GetSkill(r.Context(), h.Conn, id, su.ClassID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			httpapi.WriteError(w, http.StatusNotFound, httpapi.MsgNotFound)
		} else {
			httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		}
		return
	}

	body, ok := readBody(w, r, maxSkillBodyBytes)
	if !ok {
		return
	}
	var in skillBody
	if err := json.Unmarshal(body, &in); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	s, err := ParseSkill(in.Source)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.checkSkillMCPTools(w, r, su.ClassID, s.ToolsJSON) {
		return
	}
	s.ID = id
	s.ClassID = su.ClassID

	if err := db.UpdateSkill(r.Context(), h.Conn, s); err != nil {
		writeWriteError(w, err, "已存在同名技能")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]int{"id": id})
}

// DeleteSkill implements DELETE /api/skills/{id} (teacher only). The database
// cascades the agent bindings (spec: 删除技能同时解除绑定).
//
// @Summary 删除技能
// @Description 仅教师可用。删除技能会同时解除它与智能体的绑定；他班与不存在的编号是同一个 404。
// @Tags 技能
// @Produce json
// @Param id path int true "技能编号"
// @Success 200 {object} map[string]bool "ok 恒为 true"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 403 {object} httpapi.errorBody "学生无权操作"
// @Failure 404 {object} httpapi.errorBody "技能不存在（含他班）"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/skills/{id} [delete]
func (h *Handlers) DeleteSkill(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())

	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := db.DeleteSkill(r.Context(), h.Conn, id, su.ClassID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			httpapi.WriteError(w, http.StatusNotFound, httpapi.MsgNotFound)
		} else {
			httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		}
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
