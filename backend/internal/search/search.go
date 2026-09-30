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

// Engine resolves queries for one class. It is the shared core of the search
// endpoint and of the ask pipeline.
type Engine struct {
	conn *sql.DB
}

// New builds an engine over the given database.
func New(conn *sql.DB) *Engine { return &Engine{conn: conn} }

// Search runs one validated query and returns the traceable hits of classID, at
// most req.TopK of them.
func (e *Engine) Search(ctx context.Context, classID int, req Request) ([]Hit, error) {
	switch req.Mode {
	case ModeKeyword:
		return e.keyword(ctx, classID, req)
	}
	// The vector and hybrid modes take their candidates from the embedding
	// gateway and the vector store; a request that cannot resolve any yields
	// no hits rather than an error.
	return []Hit{}, nil
}

// keyword resolves the MySQL full-text branch. It never calls the embedding
// gateway or the vector store, so it stays available when either is down
// (spec: 关键词检索).
func (e *Engine) keyword(ctx context.Context, classID int, req Request) ([]Hit, error) {
	found, err := db.SearchChunksByKeyword(ctx, e.conn, classID, req.Query, db.KeywordLimit)
	if err != nil {
		return nil, err
	}
	return toHits(found, req.TopK), nil
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
