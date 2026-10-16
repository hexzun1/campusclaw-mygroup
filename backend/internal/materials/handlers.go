// Package materials implements the read/write HTTP handlers for materials
// and their knowledge entries. Every handler trusts only the session
// attached to the request context (see httpapi.SessionUserFromContext) for
// role and class — request parameters claiming a class or role are ignored,
// per design.md Decision 4.
package materials

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
	"campusclaw/backend/internal/index"
)

type Handlers struct {
	Conn *sql.DB
	// Indexer runs the vector pipeline after a material is committed. It is the
	// only place that embeds text and writes vectors during upload.
	Indexer *index.Indexer
	// Vectors is used by the reindex flow, which deletes a material's vectors
	// before swapping its chunks.
	Vectors VectorStore
}

func NewHandlers(conn *sql.DB, indexer *index.Indexer, vectors VectorStore) *Handlers {
	return &Handlers{Conn: conn, Indexer: indexer, Vectors: vectors}
}

// List returns materials for the caller's own class only. Any class_id in
// the query string is ignored.
//
// @Summary 材料列表
// @Description 返回本班材料（可按标题关键字过滤）；班级范围只取自 token，请求里的 class_id 一律被忽略。
// @Tags 材料
// @Produce json
// @Param q query string false "标题关键字"
// @Success 200 {array} map[string]interface{} "id、title、class_id、created_at"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/materials [get]
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())

	q := r.URL.Query().Get("q")
	list, err := db.ListMaterials(r.Context(), h.Conn, su.ClassID, q)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	type item struct {
		ID        int    `json:"id"`
		Title     string `json:"title"`
		ClassID   int    `json:"class_id"`
		CreatedAt string `json:"created_at"`
	}
	out := make([]item, 0, len(list))
	for _, m := range list {
		out = append(out, item{ID: m.ID, Title: m.Title, ClassID: m.ClassID, CreatedAt: m.CreatedAt.Format("2006-01-02T15:04:05Z07:00")})
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

// loadOwnClassMaterial fetches a material by path id and enforces class
// isolation: not-found and cross-class both resolve to the same 404, with
// no distinguishing information (design.md Decision 4).
func (h *Handlers) loadOwnClassMaterial(w http.ResponseWriter, r *http.Request) *db.Material {
	su := httpapi.SessionUserFromContext(r.Context())

	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, httpapi.MsgNotFound)
		return nil
	}

	m, err := db.GetMaterialByID(r.Context(), h.Conn, id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			httpapi.WriteError(w, http.StatusNotFound, httpapi.MsgNotFound)
			return nil
		}
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return nil
	}

	if m.ClassID != su.ClassID {
		httpapi.WriteError(w, http.StatusNotFound, httpapi.MsgNotFound)
		return nil
	}
	return m
}

// Detail returns title, class, upload time and knowledge body, together with
// the aggregate index state of the material's chunks. It MUST NOT expose
// stored_name, any filesystem path, a vector or the vector store's address.
//
// @Summary 材料详情
// @Description 返回标题、上传时间、知识条目正文与聚合索引状态；不存在的编号与他班编号都是同一个 404。
// @Tags 材料
// @Produce json
// @Param id path int true "材料编号"
// @Success 200 {object} map[string]interface{} "id、title、class_id、created_at、body、index_status、chunk_count、chunk_strategy"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 404 {object} httpapi.errorBody "记录不存在（含他班）"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/materials/{id} [get]
func (h *Handlers) Detail(w http.ResponseWriter, r *http.Request) {
	m := h.loadOwnClassMaterial(w, r)
	if m == nil {
		return
	}

	entry, err := db.GetKnowledgeEntryByMaterialID(r.Context(), h.Conn, m.ID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			httpapi.WriteError(w, http.StatusNotFound, httpapi.MsgNotFound)
			return
		}
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	// The aggregate is what the caller needs to know: any failed chunk makes the
	// material failed, otherwise any pending chunk makes it pending (spec: 索引
	// 状态与重建). loadOwnClassMaterial already proved the material belongs to the
	// caller's class, so its class_id is safe to filter by.
	stats, err := db.GetChunkStats(r.Context(), h.Conn, m.ID, m.ClassID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			httpapi.WriteError(w, http.StatusNotFound, httpapi.MsgNotFound)
			return
		}
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"id":             m.ID,
		"title":          m.Title,
		"class_id":       m.ClassID,
		"created_at":     m.CreatedAt,
		"body":           entry.BodyText,
		"index_status":   stats.Status,
		"chunk_count":    stats.Count,
		"chunk_strategy": stats.Strategy,
	})
}
