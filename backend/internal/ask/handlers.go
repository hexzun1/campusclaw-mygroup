package ask

import (
	"io"
	"log"
	"net/http"

	"campusclaw/backend/internal/httpapi"
	"campusclaw/backend/internal/search"
)

// maxAskBodyBytes bounds a request body: a 1000-character question plus at most
// ten 2000-character history messages, with JSON overhead. Anything larger is a
// malformed or hostile request.
const maxAskBodyBytes = 64 << 10

// Handlers wraps the pipeline in HTTP.
type Handlers struct {
	Engine *Engine
}

// NewHandlers builds the ask handlers.
func NewHandlers(engine *Engine) *Handlers { return &Handlers{Engine: engine} }

// askResponse is the body of POST /api/ask: one complete answer, never a
// stream, plus the citations the server decided on. citations[i] is referenced
// as [i+1] in the answer.
type askResponse struct {
	Answer    string       `json:"answer"`
	Citations []search.Hit `json:"citations"`
}

// Ask implements POST /api/ask: it answers the question from the caller's class
// knowledge base, with citations, or with the not-found hint when retrieval
// yields nothing.
//
// The class scope is the session's alone; a class_id in the query string,
// headers or body is ignored (spec: 检索的权限与班级范围).
func (h *Handlers) Ask(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAskBodyBytes))
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "请求格式错误")
		return
	}

	req, err := ParseRequest(body)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	answer, citations, err := h.Engine.Ask(r.Context(), su.ClassID, req)
	if err != nil {
		// The cause is logged server-side only; the response must not name a
		// dependency, its address or its key (spec: 依赖不可用时的降级).
		log.Printf("ask: %v", err)
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	httpapi.WriteJSON(w, http.StatusOK, askResponse{Answer: answer, Citations: citations})
}
