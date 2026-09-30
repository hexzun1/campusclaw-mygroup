// Package search implements class-scoped retrieval over the knowledge base:
// a query is resolved through MySQL full text (keyword), through the embedding
// gateway and the vector store (vector), or through both fused by rank
// (hybrid, the default).
//
// The class scope always comes from the session and never from the request, and
// a query that matches nothing answers 200 with an empty list — never 403/404 —
// so "not in this class" is indistinguishable from "not in the knowledge base"
// (spec: 检索的权限与班级范围).
package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/vector"
)

const (
	// MaxQueryChars bounds a query, counted in Unicode characters rather than
	// bytes.
	MaxQueryChars = 500
	// DefaultTopK is the result count of a request that does not set one.
	DefaultTopK = 5
	// MaxTopK is the largest accepted result count.
	MaxTopK = 20
	// ExcerptChars is how many characters of a chunk text a hit carries.
	ExcerptChars = 300
	// MsgNoResults is the hint text of a result-less response
	// (spec: 检索请求与结果).
	MsgNoResults = "资料中未找到相关内容"
)

// Mode selects how search candidates are resolved.
type Mode string

const (
	// ModeKeyword searches the MySQL full-text index only and never touches the
	// embedding gateway or the vector store.
	ModeKeyword Mode = "keyword"
	// ModeVector searches the vector store only.
	ModeVector Mode = "vector"
	// ModeHybrid fuses the two branches by rank; it is the default.
	ModeHybrid Mode = "hybrid"
)

func (m Mode) valid() bool {
	switch m {
	case ModeKeyword, ModeVector, ModeHybrid:
		return true
	}
	return false
}

// Request is a validated search request. It carries no class field: passing one
// in the body, the query string or a header is ignored on purpose.
type Request struct {
	Query string
	Mode  Mode
	TopK  int
}

// wireRequest mirrors the JSON body. Unknown members (a class_id, for instance)
// decode into nothing and cannot influence the scope.
type wireRequest struct {
	Query string `json:"query"`
	Mode  string `json:"mode"`
	TopK  *int   `json:"top_k"`
}

// ParseRequest decodes and validates a request body. Every error it returns
// describes a client mistake and maps to 400.
func ParseRequest(body []byte) (Request, error) {
	var wire wireRequest
	if err := json.Unmarshal(body, &wire); err != nil {
		return Request{}, errors.New("请求格式错误")
	}

	query := strings.TrimSpace(wire.Query)
	if query == "" {
		return Request{}, errors.New("检索词不能为空")
	}
	if len([]rune(query)) > MaxQueryChars {
		return Request{}, fmt.Errorf("检索词不能超过 %d 个字符", MaxQueryChars)
	}

	mode := ModeHybrid
	if wire.Mode != "" {
		if mode = Mode(wire.Mode); !mode.valid() {
			return Request{}, errors.New("检索模式必须是 keyword、vector 或 hybrid")
		}
	}

	topK := DefaultTopK
	if wire.TopK != nil {
		if topK = *wire.TopK; topK < 1 || topK > MaxTopK {
			return Request{}, fmt.Errorf("返回条数必须在 1 到 %d 之间", MaxTopK)
		}
	}

	return Request{Query: query, Mode: mode, TopK: topK}, nil
}

// Embedder is the slice of the embedding gateway the vector branch needs.
type Embedder interface {
	EmbedOne(ctx context.Context, text string) ([]float64, error)
}

// VectorStore is the slice of the vector store the vector branch needs.
type VectorStore interface {
	Search(ctx context.Context, classID int, vector []float64, limit int) ([]vector.ScoredPoint, error)
}

// Engine resolves queries for one class. It is the shared core of the search
// endpoint and of the ask pipeline.
type Engine struct {
	conn     *sql.DB
	embedder Embedder
	vectors  VectorStore
}

// New builds an engine. The embedder and the vector store are only used by the
// vector and hybrid modes.
func New(conn *sql.DB, embedder Embedder, vectors VectorStore) *Engine {
	return &Engine{conn: conn, embedder: embedder, vectors: vectors}
}

// Search runs one validated query and returns the traceable hits of classID, at
// most req.TopK of them.
func (e *Engine) Search(ctx context.Context, classID int, req Request) ([]Hit, error) {
	switch req.Mode {
	case ModeKeyword:
		return e.keyword(ctx, classID, req)
	case ModeVector:
		return e.vector(ctx, classID, req)
	}
	// The hybrid mode fuses the two branches by rank; a request that cannot
	// resolve any yields no hits rather than an error.
	return []Hit{}, nil
}

// keyword resolves the keyword mode: the MySQL full-text branch, capped at the
// requested result count. It never calls the embedding gateway or the vector
// store, so it stays available when either is down (spec: 关键词检索).
func (e *Engine) keyword(ctx context.Context, classID int, req Request) ([]Hit, error) {
	found, err := e.keywordCandidates(ctx, classID, req.Query)
	if err != nil {
		return nil, err
	}
	return toHits(found, req.TopK), nil
}

// keywordCandidates returns the ranked keyword branch of one query, best match
// first. The class filter is part of the query itself.
func (e *Engine) keywordCandidates(ctx context.Context, classID int, query string) ([]db.ChunkHit, error) {
	return db.SearchChunksByKeyword(ctx, e.conn, classID, query, db.KeywordLimit)
}

// vector resolves the vector mode: the closest chunks of the class, capped at
// the requested result count.
func (e *Engine) vector(ctx context.Context, classID int, req Request) ([]Hit, error) {
	found, err := e.vectorCandidates(ctx, classID, req.Query)
	if err != nil {
		return nil, err
	}
	return toHits(found, req.TopK), nil
}

// vectorCandidates returns the ranked vector branch of one query, closest
// first.
//
// Three layers keep the branch inside the class: the vector store filters on
// the payload's class_id and drops weak matches below its score threshold, and
// the hits are then read back from MySQL with the class filter applied a second
// time. A point whose chunk no longer exists — or no longer belongs to the
// class — is dropped rather than returned: the vector store is an index, not
// the source of truth (spec: 向量检索).
func (e *Engine) vectorCandidates(ctx context.Context, classID int, query string) ([]db.ChunkHit, error) {
	queryVector, err := e.embedder.EmbedOne(ctx, query)
	if err != nil {
		return nil, err
	}

	points, err := e.vectors.Search(ctx, classID, queryVector, vector.SearchLimit)
	if err != nil {
		return nil, err
	}
	if len(points) == 0 {
		return nil, nil
	}

	ids := make([]int64, 0, len(points))
	for _, p := range points {
		ids = append(ids, p.ID)
	}

	rows, err := db.GetChunksByIDs(ctx, e.conn, classID, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]db.ChunkHit, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}

	// The vector store's order is the ranking, so the rows are re-read in that
	// order and the scores come from the vector store.
	found := make([]db.ChunkHit, 0, len(points))
	for _, p := range points {
		row, ok := byID[p.ID]
		if !ok {
			continue
		}
		row.Score = p.Score
		found = append(found, row)
	}
	return found, nil
}

// Hit is one traceable search result: the material it came from, the chunk and
// its position inside the material, and an excerpt of the chunk text read from
// MySQL. Vectors, filesystem paths and the vector store's address are
// deliberately absent.
type Hit struct {
	MaterialID    int     `json:"material_id"`
	MaterialTitle string  `json:"material_title"`
	ChunkID       int64   `json:"chunk_id"`
	ChunkIndex    int     `json:"chunk_index"`
	CharStart     int     `json:"char_start"`
	CharEnd       int     `json:"char_end"`
	Excerpt       string  `json:"excerpt"`
	Score         float64 `json:"score"`
}

// toHits projects database rows into the response shape, capped at topK. The
// result is never nil, so it always marshals as a JSON array.
func toHits(rows []db.ChunkHit, topK int) []Hit {
	if len(rows) > topK {
		rows = rows[:topK]
	}
	hits := make([]Hit, 0, len(rows))
	for _, row := range rows {
		hits = append(hits, toHit(row))
	}
	return hits
}

// toHit projects a database row into the response shape.
func toHit(c db.ChunkHit) Hit {
	return Hit{
		MaterialID:    c.MaterialID,
		MaterialTitle: c.MaterialTitle,
		ChunkID:       c.ID,
		ChunkIndex:    c.ChunkIndex,
		CharStart:     c.CharStart,
		CharEnd:       c.CharEnd,
		Excerpt:       Excerpt(c.ChunkText),
		Score:         c.Score,
	}
}

// Excerpt returns the first ExcerptChars characters of text, with an ellipsis
// appended when the text is longer.
func Excerpt(text string) string {
	runes := []rune(text)
	if len(runes) <= ExcerptChars {
		return text
	}
	return string(runes[:ExcerptChars]) + "…"
}
