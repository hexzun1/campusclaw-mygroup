package db

import (
	"context"
	"database/sql"
	"fmt"
)

// MaterialRef identifies one material inside one class, which is all the
// startup compensation scan needs in order to re-run the indexer.
type MaterialRef struct {
	MaterialID int
	ClassID    int
}

// ListEntriesWithoutChunks returns knowledge entries that have no chunks at
// all — iteration-1 materials and seeded ones, until the compensation scan has
// run (design.md Decision 6).
//
// afterID is a cursor: only entries with a greater id are returned, so a
// caller that pages through the table is guaranteed to make progress even when
// an entry produces no chunk at all.
func ListEntriesWithoutChunks(ctx context.Context, conn *sql.DB, afterID, limit int) ([]KnowledgeEntry, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := conn.QueryContext(ctx, `SELECT ke.id, ke.material_id, ke.class_id, ke.body_text, ke.created_at
		FROM knowledge_entries ke
		LEFT JOIN knowledge_chunks kc ON kc.knowledge_entry_id = ke.id
		WHERE kc.id IS NULL AND ke.id > ?
		ORDER BY ke.id ASC
		LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list entries without chunks: %w", err)
	}
	defer rows.Close()

	var out []KnowledgeEntry
	for rows.Next() {
		var e KnowledgeEntry
		if err := rows.Scan(&e.ID, &e.MaterialID, &e.ClassID, &e.BodyText, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan knowledge entry: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListMaterialsWithPendingChunks returns the materials that still have chunks
// whose vector was never written, so an interrupted indexing run can be
// finished at startup. Chunks marked failed are left alone: those need a
// teacher to reindex them.
//
// afterMaterialID is a cursor, as in ListEntriesWithoutChunks.
func ListMaterialsWithPendingChunks(ctx context.Context, conn *sql.DB, afterMaterialID, limit int) ([]MaterialRef, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := conn.QueryContext(ctx, `SELECT DISTINCT kc.material_id, kc.class_id
		FROM knowledge_chunks kc
		WHERE kc.index_status = 'pending' AND kc.material_id > ?
		ORDER BY kc.material_id ASC
		LIMIT ?`, afterMaterialID, limit)
	if err != nil {
		return nil, fmt.Errorf("list materials with pending chunks: %w", err)
	}
	defer rows.Close()

	var out []MaterialRef
	for rows.Next() {
		var ref MaterialRef
		if err := rows.Scan(&ref.MaterialID, &ref.ClassID); err != nil {
			return nil, fmt.Errorf("scan material ref: %w", err)
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}
