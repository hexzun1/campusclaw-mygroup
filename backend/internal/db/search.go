package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// KeywordLimit caps how many keyword candidates one search may return. The
// hybrid path fuses the top of each branch, so a generous cap keeps the
// candidate pool stable (design.md Decision 8).
const KeywordLimit = 50

// chunkHitColumns is shared by every query that returns a ChunkHit.
const chunkHitColumns = `kc.id, kc.knowledge_entry_id, kc.material_id, kc.class_id, kc.chunk_index,
		kc.char_start, kc.char_end, kc.chunk_text, kc.index_status, kc.created_at, m.title`

// SearchChunksByKeyword returns up to limit chunks of classID whose text
// matches query, best match first.
//
// Chinese full-text search uses the ngram parser, whose token size is 2 by
// default, so a one-character query can never match. Those queries fall back to
// an escaped LIKE, which still carries the same class filter
// (design.md Decision 8).
func SearchChunksByKeyword(ctx context.Context, conn *sql.DB, classID int, query string, limit int) ([]ChunkHit, error) {
	return searchChunksByKeyword(ctx, conn, classID, nil, query, limit)
}

// SearchChunksByKeywordInMaterials is SearchChunksByKeyword restricted to the
// given materials. Both the ngram and the LIKE path add the same material
// filter, so neither can return a chunk outside the set; classID is still
// required to match, so a mislabelled material id stays harmless.
func SearchChunksByKeywordInMaterials(ctx context.Context, conn *sql.DB, classID int, materialIDs []int, query string, limit int) ([]ChunkHit, error) {
	return searchChunksByKeyword(ctx, conn, classID, materialIDs, query, limit)
}

func searchChunksByKeyword(ctx context.Context, conn *sql.DB, classID int, materialIDs []int, query string, limit int) ([]ChunkHit, error) {
	if limit <= 0 {
		limit = KeywordLimit
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	filter, filterArgs := materialFilter(materialIDs)

	if len([]rune(query)) < 2 {
		return searchChunksByLike(ctx, conn, classID, filter, filterArgs, query, limit)
	}

	// The MATCH argument is repeated so the WHERE clause can filter on it;
	// ordering by the alias sorts by relevance.
	statement := `SELECT ` + chunkHitColumns + `,
			MATCH(kc.chunk_text) AGAINST(? IN NATURAL LANGUAGE MODE) AS score
		FROM knowledge_chunks kc
		JOIN materials m ON m.id = kc.material_id AND m.class_id = ?
		WHERE kc.class_id = ?` + filter + `
			AND MATCH(kc.chunk_text) AGAINST(? IN NATURAL LANGUAGE MODE)
		ORDER BY score DESC, kc.id ASC
		LIMIT ?`

	args := append([]any{query, classID, classID}, filterArgs...)
	args = append(args, query, limit)
	rows, err := conn.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}
	return scanChunkHits(rows)
}

func searchChunksByLike(ctx context.Context, conn *sql.DB, classID int, filter string, filterArgs []any, query string, limit int) ([]ChunkHit, error) {
	statement := `SELECT ` + chunkHitColumns + `, 0 AS score
		FROM knowledge_chunks kc
		JOIN materials m ON m.id = kc.material_id AND m.class_id = ?
		WHERE kc.class_id = ?` + filter + ` AND kc.chunk_text LIKE ?
		ORDER BY kc.id ASC
		LIMIT ?`

	args := append([]any{classID, classID}, filterArgs...)
	args = append(args, "%"+escapeLike(query)+"%", limit)
	rows, err := conn.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("keyword LIKE search: %w", err)
	}
	return scanChunkHits(rows)
}

// materialFilter renders the optional material restriction of the keyword
// branches: an AND clause over parameter placeholders, or nothing at all.
func materialFilter(materialIDs []int) (string, []any) {
	if len(materialIDs) == 0 {
		return "", nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(materialIDs)), ",")
	args := make([]any, len(materialIDs))
	for i, id := range materialIDs {
		args[i] = id
	}
	return " AND kc.material_id IN (" + placeholders + ")", args
}

// escapeLike neutralises the LIKE wildcards so a query containing % or _ is
// matched literally. Backslash is the default LIKE escape character, so it has
// to be escaped first.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func scanChunkHits(rows *sql.Rows) ([]ChunkHit, error) {
	defer rows.Close()

	var out []ChunkHit
	for rows.Next() {
		var h ChunkHit
		if err := rows.Scan(&h.ID, &h.KnowledgeEntryID, &h.MaterialID, &h.ClassID, &h.ChunkIndex,
			&h.CharStart, &h.CharEnd, &h.ChunkText, &h.IndexStatus, &h.CreatedAt,
			&h.MaterialTitle, &h.Score); err != nil {
			return nil, fmt.Errorf("scan chunk hit: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
