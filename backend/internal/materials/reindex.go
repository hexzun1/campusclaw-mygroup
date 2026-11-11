package materials

import (
	"errors"
	"io"
	"log"
	"net/http"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
	"campusclaw/backend/internal/index"
	"campusclaw/backend/internal/knowledge"
)

// maxReindexBodyBytes bounds a reindex request body: it carries only chunking
// parameters, so anything larger is a malformed or hostile request.
const maxReindexBodyBytes = 8 << 10

// Reindex rebuilds one material's chunks under a new chunking strategy
// (spec: 索引状态与重建).
//
// Role, class and parameter validation all happen here, before the rebuild
// starts. The rebuild itself runs in internal/index's coordinator so it shares
// the per-material lock with ordinary indexing (upload and compensation); its
// order and failure semantics live there. The job of this handler is only to
// validate the request, hand the server-resolved material and class to the
// coordinator, and write the response after the lock has been released.
//
// @Summary 重建索引
// @Description 仅教师可用。先删除该材料的旧向量，再按新切分参数替换切片并重新索引；空 body 按 auto。参数不合法 400 且磁盘、数据库、向量库均不被改动。
// @Tags 材料
// @Accept json
// @Produce json
// @Param id path int true "材料编号"
// @Param params body map[string]interface{} false "切分参数对象：strategy、chunk_size、overlap_percent、remove_url、remove_email、collapse_whitespace"
// @Success 200 {object} map[string]interface{} "id、title、index_status、chunk_count、chunk_strategy"
// @Failure 400 {object} httpapi.errorBody "切分参数不合法"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 403 {object} httpapi.errorBody "学生无权重建"
// @Failure 404 {object} httpapi.errorBody "记录不存在（含他班）"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/materials/{id}/reindex [post]
func (h *Handlers) Reindex(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())

	// Role check happens before the request body is read at all.
	if su.Role != db.RoleTeacher {
		httpapi.WriteError(w, http.StatusForbidden, httpapi.MsgForbidden)
		return
	}

	// Class isolation: the material and its class come from the session-scoped
	// lookup, never from the request body. Cross-class and missing are the same
	// 404.
	m := h.loadOwnClassMaterial(w, r)
	if m == nil {
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxReindexBodyBytes))
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "请求格式错误")
		return
	}

	// Parameters are validated before anything destructive can start.
	params, err := knowledge.ParseParamsJSON(body)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "切分参数不合法："+err.Error())
		return
	}

	if h.Indexer == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	stats, err := h.Indexer.Reindex(r.Context(), index.ReindexRequest{
		MaterialID: m.ID,
		ClassID:    m.ClassID,
		Params:     params,
		Store:      index.SQLReindexStore{Conn: h.Conn},
		Vectors:    h.Vectors,
	})
	if err != nil {
		if errors.Is(err, index.ErrReindexNotFound) {
			httpapi.WriteError(w, http.StatusNotFound, httpapi.MsgNotFound)
			return
		}
		log.Printf("reindex material %d: %v", m.ID, err)
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	// The stats snapshot was read inside the critical section; only the HTTP
	// write happens after the lock is released.
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"id":             m.ID,
		"title":          m.Title,
		"index_status":   stats.Status,
		"chunk_count":    stats.Count,
		"chunk_strategy": stats.Strategy,
	})
}
