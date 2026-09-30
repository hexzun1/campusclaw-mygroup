package materials

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
	"campusclaw/backend/internal/knowledge"
)

// maxReindexBodyBytes bounds a reindex request body: it carries only chunking
// parameters, so anything larger is a malformed or hostile request.
const maxReindexBodyBytes = 8 << 10

// failMarkTimeout bounds the best-effort status write that records a failure,
// on a context detached from the request.
const failMarkTimeout = 10 * time.Second

// VectorStore is the slice of the vector store the handlers need.
type VectorStore interface {
	DeleteByMaterial(ctx context.Context, materialID, classID int) error
}

// Reindex rebuilds one material's chunks under a new chunking strategy
// (spec: 索引状态与重建).
//
// The order is deliberate (design.md Decision 7): the vectors go first, then
// the chunks are swapped inside a transaction, then the new chunks are indexed.
// Deleting first means no point is ever left pointing at a chunk that has been
// replaced. A vector-store failure answers 503 without touching MySQL at all;
// a failure of the MySQL step leaves the old chunks in place and marks them
// failed, because their vectors are already gone.
func (h *Handlers) Reindex(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())

	// Role check happens before the request body is read at all.
	if su.Role != db.RoleTeacher {
		httpapi.WriteError(w, http.StatusForbidden, httpapi.MsgForbidden)
		return
	}

	m := h.loadOwnClassMaterial(w, r)
	if m == nil {
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxReindexBodyBytes))
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "请求格式错误")
		return
	}

	params, err := knowledge.ParseParamsJSON(body)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "切分参数不合法："+err.Error())
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

	// 1. The old vectors go first: nothing may point at chunks that are about to
	// be replaced. On failure MySQL is left completely untouched.
	if h.Vectors != nil {
		if err := h.Vectors.DeleteByMaterial(r.Context(), m.ID, m.ClassID); err != nil {
			log.Printf("reindex material %d: %v", m.ID, err)
			httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
			return
		}
	}

	// 2. Swap the chunks in one transaction.
	chunks := toDBChunks(knowledge.ChunkBody(entry.BodyText, params))

	if err := db.ReplaceChunksForMaterial(r.Context(), h.Conn, m.ID, m.ClassID,
		string(params.Strategy), params.ParamsJSON(), chunks); err != nil {
		log.Printf("reindex material %d: replace chunks: %v", m.ID, err)
		// The vectors are gone and the old chunks are still here, so the honest
		// state is "failed"; a teacher can reindex again (best effort).
		h.markChunksFailed(r.Context(), m.ID, m.ClassID)
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	// 3. Index the new chunks. A failure here is recorded on the chunks and does
	// not turn the reindex into an error.
	if h.Indexer != nil {
		if err := h.Indexer.IndexMaterial(r.Context(), m.ID, m.ClassID); err != nil {
			log.Printf("reindex material %d: index: %v", m.ID, err)
		}
	}

	stats, err := db.GetChunkStats(r.Context(), h.Conn, m.ID, m.ClassID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"id":             m.ID,
		"title":          m.Title,
		"index_status":   stats.Status,
		"chunk_count":    stats.Count,
		"chunk_strategy": stats.Strategy,
	})
}

// markChunksFailed reports the failure on every chunk of a material. It is
// best effort: it runs on a detached context and a failure here is only logged.
func (h *Handlers) markChunksFailed(ctx context.Context, materialID, classID int) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failMarkTimeout)
	defer cancel()

	chunks, err := db.ListChunksByMaterial(ctx, h.Conn, materialID, classID, "")
	if err != nil {
		log.Printf("reindex material %d: list chunks for failure marking: %v", materialID, err)
		return
	}
	ids := make([]int64, 0, len(chunks))
	for _, c := range chunks {
		ids = append(ids, c.ID)
	}
	if err := db.UpdateChunksStatus(ctx, h.Conn, ids, db.IndexFailed); err != nil {
		log.Printf("reindex material %d: mark %d chunks failed: %v", materialID, len(ids), err)
	}
}
