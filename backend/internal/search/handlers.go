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
}

// Search implements POST /api/search: it validates the request and answers with
// the traceable hits of the caller's class.
//
// The class scope is the session's alone; a class_id in the query string,
// headers or body is ignored (spec: 检索的权限与班级范围).
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
