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
	// only place that talks to the embedding gateway and the vector store.
	Indexer *index.Indexer
}

func NewHandlers(conn *sql.DB, indexer *index.Indexer) *Handlers {
	return &Handlers{Conn: conn, Indexer: indexer}
}

// List returns materials for the caller's own class only. Any class_id in
// the query string is ignored.
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

// Detail returns title, class, upload time and knowledge body. It MUST NOT
// expose stored_name or any filesystem path.
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

	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"id":         m.ID,
		"title":      m.Title,
		"class_id":   m.ClassID,
		"created_at": m.CreatedAt,
		"body":       entry.BodyText,
	})
}
