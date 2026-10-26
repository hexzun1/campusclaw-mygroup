package mcp

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
)

const (
	maxServerBodyBytes  = 8 << 10
	maxDescriptionRunes = 1024
	maxSchemaBytes      = 8 << 10
	serverNamePattern   = `^[a-z][a-z0-9-]{0,31}$`
	toolNamePattern     = `^[a-z][a-z0-9_]{0,63}$`
)

var (
	serverNameRE = regexp.MustCompile(serverNamePattern)
	toolNameRE   = regexp.MustCompile(toolNamePattern)
)

// Handlers is the teacher-facing management surface for MCP servers and the
// audit list. Class and role come only from the session (design.md Decision 5).
type Handlers struct {
	Conn   *sql.DB
	Client *Client
}

func NewHandlers(conn *sql.DB, client *Client) *Handlers {
	return &Handlers{Conn: conn, Client: client}
}

type serverBody struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	Token   string `json:"token"`
} // @name MCPServerRequest

type deleteConflict struct {
	Error  string   `json:"error"`
	Skills []string `json:"skills"`
} // @name MCPServerDeleteConflict

func readJSONBody(w http.ResponseWriter, r *http.Request, limit int64, out any) bool {
	body, err := readBody(w, r, limit)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "请求格式错误")
		return false
	}
	if err := json.Unmarshal(body, out); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "请求格式错误")
		return false
	}
	return true
}

func readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
}

// ListServers implements GET /api/mcp/servers.
//
// @Summary 工具服务器列表
// @Description 列出本班已登记的 MCP 工具服务器；响应不含令牌，只给出 has_token。
// @Tags MCP
// @Produce json
// @Success 200 {array} db.MCPServer
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 403 {object} httpapi.errorBody "仅教师可管理"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/mcp/servers [get]
func (h *Handlers) ListServers(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())
	servers, err := db.ListServers(r.Context(), h.Conn, su.ClassID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, servers)
}

// CreateServer implements POST /api/mcp/servers.
//
// @Summary 登记工具服务器
// @Description 仅教师。令牌只写不读：响应与错误信息都不含令牌原文。
// @Tags MCP
// @Accept json
// @Produce json
// @Param body body serverBody true "name（^[a-z][a-z0-9-]{0,31}$）、base_url（http/https、不含账号口令、不以 /mcp 结尾）、token（1–512）"
// @Success 201 {object} db.MCPServer
// @Failure 400 {object} httpapi.errorBody "名称、地址或令牌不合法"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 403 {object} httpapi.errorBody "仅教师可管理"
// @Failure 409 {object} httpapi.errorBody "同名服务器已存在"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/mcp/servers [post]
func (h *Handlers) CreateServer(w http.ResponseWriter, r *http.Request) {
	var body serverBody
	if !readJSONBody(w, r, maxServerBodyBytes, &body) {
		return
	}
	su := httpapi.SessionUserFromContext(r.Context())

	if !serverNameRE.MatchString(body.Name) {
		httpapi.WriteError(w, http.StatusBadRequest,
			"名称必须以小写字母开头，只含小写字母、数字与连字符，且不超过 32 个字符")
		return
	}
	if err := validateBaseURL(body.BaseURL); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if n := utf8.RuneCountInString(body.Token); n == 0 || n > 512 {
		httpapi.WriteError(w, http.StatusBadRequest, "令牌长度必须在 1 到 512 个字符之间")
		return
	}

	id, err := db.CreateServer(r.Context(), h.Conn, su.ClassID, body.Name, body.BaseURL, body.Token, su.UserID, "")
	if errors.Is(err, db.ErrDuplicateName) {
		httpapi.WriteError(w, http.StatusConflict, "同名服务器已存在")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}
	server, err := db.GetServer(r.Context(), h.Conn, id, su.ClassID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, server)
}

func validateBaseURL(raw string) error {
	if len(raw) > 200 {
		return errors.New("服务器地址不能超过 200 个字符")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("服务器地址必须是 http 或 https 开头的有效地址")
	}
	if parsed.User != nil {
		return errors.New("服务器地址不能包含用户名或口令")
	}
	if strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/mcp") {
		return errors.New("服务器地址不要以 /mcp 结尾，主机自行拼接 /mcp")
	}
	return nil
}

// DeleteServer implements DELETE /api/mcp/servers/{id}. A referenced server is
// a 409 naming every skill that still uses it.
//
// @Summary 删除工具服务器
// @Tags MCP
// @Produce json
// @Param id path int true "服务器编号"
// @Success 204 "已删除"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 403 {object} httpapi.errorBody "仅教师可管理"
// @Failure 404 {object} httpapi.errorBody "记录不存在"
// @Failure 409 {object} deleteConflict "仍被技能引用"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/mcp/servers/{id} [delete]
func (h *Handlers) DeleteServer(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	server, err := db.GetServer(r.Context(), h.Conn, id, su.ClassID)
	if err != nil {
		writeLookupError(w, err)
		return
	}
	skills, err := db.ListSkillsReferencingServer(r.Context(), h.Conn, su.ClassID, server.Name)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}
	if len(skills) > 0 {
		httpapi.WriteJSON(w, http.StatusConflict, deleteConflict{
			Error:  "服务器仍被技能引用，请先解除引用",
			Skills: skills,
		})
		return
	}
	if err := db.DeleteServer(r.Context(), h.Conn, id, su.ClassID); err != nil {
		writeLookupError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Connect implements POST /api/mcp/servers/{id}/connect: handshake, discovery,
// whole-cache replacement on success, error state plus empty cache on failure.
//
// @Summary 连接并发现工具
// @Description 完整握手后整体替换工具缓存；失败返回 502，服务器状态置为 error 且清空缓存。
// @Tags MCP
// @Produce json
// @Param id path int true "服务器编号"
// @Success 200 {array} db.MCPTool "发现到的工具"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 403 {object} httpapi.errorBody "仅教师可管理"
// @Failure 404 {object} httpapi.errorBody "记录不存在"
// @Failure 502 {object} httpapi.errorBody "无法连接或令牌被拒绝"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/mcp/servers/{id}/connect [post]
func (h *Handlers) Connect(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	server, err := db.GetServer(r.Context(), h.Conn, id, su.ClassID)
	if err != nil {
		writeLookupError(w, err)
		return
	}
	token, err := db.GetServerToken(r.Context(), h.Conn, id, su.ClassID)
	if err != nil {
		writeLookupError(w, err)
		return
	}
	discovered, err := h.Client.Discover(r.Context(), server.BaseURL, token)
	if err != nil {
		message := truncateRunes(err.Error(), 255)
		if markErr := db.MarkServerError(r.Context(), h.Conn, id, su.ClassID, message); markErr != nil {
			log.Printf("mcp: mark server %d error: %v", id, markErr)
		}
		httpapi.WriteError(w, http.StatusBadGateway, message)
		return
	}

	tools := filterTools(id, discovered)
	if err := db.ReplaceServerTools(r.Context(), h.Conn, id, su.ClassID, tools); err != nil {
		log.Printf("mcp: replace tools for server %d: %v", id, err)
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, tools)
}

// ListTools implements GET /api/mcp/servers/{id}/tools.
//
// @Summary 查看已发现的工具
// @Description 返回上一次连接成功时缓存的工具清单；未连接时为空数组。
// @Tags MCP
// @Produce json
// @Param id path int true "服务器编号"
// @Success 200 {array} db.MCPTool
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 403 {object} httpapi.errorBody "仅教师可管理"
// @Failure 404 {object} httpapi.errorBody "记录不存在"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/mcp/servers/{id}/tools [get]
func (h *Handlers) ListTools(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := db.GetServer(r.Context(), h.Conn, id, su.ClassID); err != nil {
		writeLookupError(w, err)
		return
	}
	tools, err := db.ListServerTools(r.Context(), h.Conn, id, su.ClassID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, tools)
}

// Disconnect implements POST /api/mcp/servers/{id}/disconnect.
//
// @Summary 断开工具服务器
// @Description 清空工具缓存并把状态置为 disconnected。
// @Tags MCP
// @Param id path int true "服务器编号"
// @Success 204 "已断开"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 403 {object} httpapi.errorBody "仅教师可管理"
// @Failure 404 {object} httpapi.errorBody "记录不存在"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/mcp/servers/{id}/disconnect [post]
func (h *Handlers) Disconnect(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := db.DisconnectServer(r.Context(), h.Conn, id, su.ClassID); err != nil {
		writeLookupError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListCalls implements GET /api/mcp/calls?limit=50.
//
// @Summary 本班 MCP 调用审计
// @Description 仅教师；按时间倒序返回本班记录，limit 缺省 50、范围 1–200。每条的 tool 为合并后的「工具名@服务器名」，不含参数与结果原文。
// @Tags MCP
// @Produce json
// @Param limit query int false "条数，缺省 50，范围 1–200"
// @Success 200 {array} db.MCPToolCall
// @Failure 400 {object} httpapi.errorBody "limit 越界"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 403 {object} httpapi.errorBody "仅教师可查看"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/mcp/calls [get]
func (h *Handlers) ListCalls(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			httpapi.WriteError(w, http.StatusBadRequest, "limit 必须是 1 到 200 之间的整数")
			return
		}
		limit = n
	}
	calls, err := db.ListToolCalls(r.Context(), h.Conn, su.ClassID, limit)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, calls)
}

func filterTools(serverID int, discovered []Tool) []db.MCPTool {
	out := make([]db.MCPTool, 0, len(discovered))
	for _, tool := range discovered {
		if !toolNameRE.MatchString(tool.Name) {
			log.Printf("mcp: dropped tool with invalid name %q from server %d", tool.Name, serverID)
			continue
		}
		if len(tool.InputSchema) > maxSchemaBytes {
			log.Printf("mcp: dropped tool %q from server %d: inputSchema exceeds %d bytes", tool.Name, serverID, maxSchemaBytes)
			continue
		}
		out = append(out, db.MCPTool{
			Name:        tool.Name,
			Description: truncateRunes(tool.Description, maxDescriptionRunes),
			InputSchema: tool.InputSchema,
		})
	}
	return out
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

func pathID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, httpapi.MsgNotFound)
		return 0, false
	}
	return id, true
}

func writeLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, httpapi.MsgNotFound)
		return
	}
	if errors.Is(err, db.ErrDuplicateName) {
		httpapi.WriteError(w, http.StatusConflict, "同名服务器已存在")
		return
	}
	httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
}
