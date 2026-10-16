package tutor

import (
	"strings"

	"campusclaw/backend/internal/search"
)

// Source is one retained retrieval result, numbered for the turn's citation
// list. Its JSON shape IS the citation — the marker [n] in an answer must
// point at citations[n-1] — while Text keeps the full chunk body server-side
// for the gateway message and is never serialized.
type Source struct {
	Index         int     `json:"index"`
	MaterialID    int     `json:"material_id"`
	MaterialTitle string  `json:"material_title"`
	ChunkID       int64   `json:"chunk_id"`
	ChunkIndex    int     `json:"chunk_index"`
	CharStart     int     `json:"char_start"`
	CharEnd       int     `json:"char_end"`
	Excerpt       string  `json:"excerpt"`
	Score         float64 `json:"score"`
	Text          string  `json:"-"`
}

// BuildCitations applies the retrieval post-processing of one turn (spec:
// 资料检索与编号): hits without a body are dropped first, then — when the
// routed skill declared a 资料 scope — only hits whose material title contains
// that word survive, and the survivors are renumbered from 1 in their retained
// order, so citation n is always the n-th kept chunk. The result is never nil.
func BuildCitations(hits []search.Hit, subjectText string) []Source {
	out := make([]Source, 0, len(hits))
	for _, hit := range hits {
		if strings.TrimSpace(hit.Text) == "" {
			continue
		}
		if subjectText != "" && !strings.Contains(hit.MaterialTitle, subjectText) {
			continue
		}
		out = append(out, Source{
			Index:         len(out) + 1,
			MaterialID:    hit.MaterialID,
			MaterialTitle: hit.MaterialTitle,
			ChunkID:       hit.ChunkID,
			ChunkIndex:    hit.ChunkIndex,
			CharStart:     hit.CharStart,
			CharEnd:       hit.CharEnd,
			Excerpt:       search.Excerpt(hit.Text),
			Score:         hit.Score,
			Text:          hit.Text,
		})
	}
	return out
}
