package search

import (
	"io"
	"log"
	"net/http"

	"campusclaw/backend/internal/httpapi"
)

// maxSearchBodyBytes bounds a search request body: it carries a query, a mode
// and a result count, nothing more.
const maxSearchBodyBytes = 8 << 10

// Handlers wraps an engine in HTTP.
type Handlers struct {
	Engine *Engine
}

// NewHandlers builds the retrieval handlers.
func NewHandlers(engine *Engine) *Handlers { return &Handlers{Engine: engine} }

// searchResponse is the body of POST /api/search. A result-less response keeps
// the 200 status and adds the hint text, so an empty result never discloses
// whether the content exists somewhere the caller cannot see.
type searchResponse struct {
	Mode    Mode   `json:"mode"`
	Hits    []Hit  `json:"hits"`
	Message string `json:"message,omitempty"`
} // @name SearchResponse

// Search implements POST /api/search: it validates the request and answers with
// the traceable hits of the caller's class.
//
// The class scope is the session's alone; a class_id in the query string,
// headers or body is ignored (spec: 检索的权限与班级范围).
//
// @Summary 知识检索
// @Description 三种模式：keyword（MySQL 全文，依赖最少）、vector（向量近邻）、hybrid（两路融合，缺省）。班级范围只取自 token。没有命中时仍是 200，附带 message 提示。
// @Tags 检索
// @Accept json
// @Produce json
// @Param request body wireRequest true "检索请求"
// @Success 200 {object} searchResponse "mode、hits、message"
// @Failure 400 {object} httpapi.errorBody "检索词为空或超长，或模式、条数不合法"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 503 {object} httpapi.errorBody "检索依赖不可用（vector / hybrid 需要嵌入网关与 Qdrant）"
// @Security BearerAuth
// @Router /api/search [post]
func (h *Handlers) Search(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSearchBodyBytes))
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "请求格式错误")
		return
	}

	req, err := ParseRequest(body)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	hits, err := h.Engine.Search(r.Context(), su.ClassID, req)
	if err != nil {
		// The cause is logged server-side only; the response must not name a
		// dependency, its address or its key (spec: 依赖不可用时的降级).
		log.Printf("search: %v", err)
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	writeResults(w, req.Mode, hits)
}

// writeResults renders a search outcome: the hits, or the not-found hint when
// there are none.
func writeResults(w http.ResponseWriter, mode Mode, hits []Hit) {
	resp := searchResponse{Mode: mode, Hits: hits}
	if len(hits) == 0 {
		resp.Message = MsgNoResults
	}
	httpapi.WriteJSON(w, http.StatusOK, resp)
}
