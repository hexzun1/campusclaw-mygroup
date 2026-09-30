package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// IndexStatus is the vector-index state of a single chunk.
type IndexStatus string

const (
	// IndexPending means the chunk row exists but its vector is not written yet.
	IndexPending IndexStatus = "pending"
	// IndexIndexed means the vector was written to the vector store.
	IndexIndexed IndexStatus = "indexed"
	// IndexFailed means embedding or the vector write failed; a teacher can
	// recover it with a reindex.
	IndexFailed IndexStatus = "failed"
)

// Chunk is one row of knowledge_chunks: a slice of a knowledge entry's body.
// CharStart/CharEnd are Unicode character offsets into the untouched body text,
// left-closed and right-open.
type Chunk struct {
	ID               int64
	KnowledgeEntryID int
	MaterialID       int
	ClassID          int
	ChunkIndex       int
	CharStart        int
	CharEnd          int
	ChunkText        string
	IndexStatus      IndexStatus
	CreatedAt        time.Time
}

// ChunkHit is a chunk joined with the title of its material, which is what
// search results and answers need to be traceable. Score carries the relevance
// value of the mode that produced the hit (full-text relevance, cosine or RRF
// sum); it is informational and its scale differs per mode
// (design.md Decision 8).
type ChunkHit struct {
	Chunk
	MaterialTitle string
	Score         float64
}

// ChunkStats is the aggregate index state of one material.
type ChunkStats struct {
	Count    int
	Status   IndexStatus
	Strategy string
}

// insertChunkBatchSize bounds how many rows go into one INSERT. A 2 MB body
// yields roughly 900 chunks, which is well within max_allowed_packet, but
// keeping the statement small keeps failures bounded too.
const insertChunkBatchSize = 100

// sqlExecutor is implemented by both *sql.DB and *sql.Tx, so the chunk writers
// can run either standalone or inside a caller's transaction.
type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

const insertChunkColumns = `INSERT INTO knowledge_chunks
	(knowledge_entry_id, material_id, class_id, chunk_index, char_start, char_end, chunk_text, index_status)
	VALUES `

// InsertChunks writes chunks in one transaction.
func InsertChunks(ctx context.Context, conn *sql.DB, chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin chunk tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := insertChunks(ctx, tx, chunks); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit chunk tx: %w", err)
	}
	return nil
}

// insertChunks is the transaction-agnostic writer used by InsertChunks,
// ReplaceChunksForMaterial and the upload transaction.
func insertChunks(ctx context.Context, ex sqlExecutor, chunks []Chunk) error {
	for start := 0; start < len(chunks); start += insertChunkBatchSize {
		end := min(start+insertChunkBatchSize, len(chunks))
		batch := chunks[start:end]

		var sb strings.Builder
		sb.WriteString(insertChunkColumns)
		args := make([]any, 0, len(batch)*8)
		for i, c := range batch {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(?, ?, ?, ?, ?, ?, ?, ?)")
			status := c.IndexStatus
			if status == "" {
				status = IndexPending
			}
			args = append(args, c.KnowledgeEntryID, c.MaterialID, c.ClassID, c.ChunkIndex,
				c.CharStart, c.CharEnd, c.ChunkText, status)
		}
		if _, err := ex.ExecContext(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("insert chunks: %w", err)
		}
	}
	return nil
}

// ListChunksByMaterial returns a material's chunks in index order. classID MUST
// come from the caller's session; a status filter is applied only when it is
// not empty.
func ListChunksByMaterial(ctx context.Context, conn *sql.DB, materialID, classID int, status IndexStatus) ([]Chunk, error) {
	query := `SELECT id, knowledge_entry_id, material_id, class_id, chunk_index,
			char_start, char_end, chunk_text, index_status, created_at
		FROM knowledge_chunks WHERE material_id = ? AND class_id = ?`
	args := []any{materialID, classID}
	if status != "" {
		query += " AND index_status = ?"
		args = append(args, status)
	}
	query += " ORDER BY chunk_index ASC"

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list chunks: %w", err)
	}
	defer rows.Close()

	var out []Chunk
	for rows.Next() {
		c, err := scanChunk(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetChunksByIDs returns the chunks whose MySQL primary keys are in ids, joined
// with their material title.
//
// classID is enforced twice on purpose (design.md Decision 8): the WHERE clause
// filters the chunk's own class_id and the JOIN filters the material's
// class_id, so neither a mislabelled chunk row nor a stale vector-store point
// can pull another class's content into the result.
func GetChunksByIDs(ctx context.Context, conn *sql.DB, classID int, ids []int64) ([]ChunkHit, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	query := `SELECT kc.id, kc.knowledge_entry_id, kc.material_id, kc.class_id, kc.chunk_index,
			kc.char_start, kc.char_end, kc.chunk_text, kc.index_status, kc.created_at, m.title
		FROM knowledge_chunks kc
		JOIN materials m ON m.id = kc.material_id AND m.class_id = ?
		WHERE kc.class_id = ? AND kc.id IN (` + placeholders(len(ids)) + `)`

	args := make([]any, 0, len(ids)+2)
	args = append(args, classID, classID)
	for _, id := range ids {
		args = append(args, id)
	}

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get chunks by ids: %w", err)
	}
	defer rows.Close()

	var out []ChunkHit
	for rows.Next() {
		var h ChunkHit
		if err := rows.Scan(&h.ID, &h.KnowledgeEntryID, &h.MaterialID, &h.ClassID, &h.ChunkIndex,
			&h.CharStart, &h.CharEnd, &h.ChunkText, &h.IndexStatus, &h.CreatedAt, &h.MaterialTitle); err != nil {
			return nil, fmt.Errorf("scan chunk hit: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// GetChunkStats aggregates the index state and chunk count of one material and
// reports the strategy used for its most recent chunking.
//
// Aggregate rule (spec: 索引状态与重建): any failed chunk makes the material
// failed, otherwise any pending chunk makes it pending, otherwise indexed.
func GetChunkStats(ctx context.Context, conn *sql.DB, materialID, classID int) (ChunkStats, error) {
	row := conn.QueryRowContext(ctx, `SELECT ke.chunk_strategy,
			COUNT(kc.id),
			COALESCE(SUM(kc.index_status = 'failed'), 0),
			COALESCE(SUM(kc.index_status = 'pending'), 0)
		FROM knowledge_entries ke
		LEFT JOIN knowledge_chunks kc
			ON kc.knowledge_entry_id = ke.id AND kc.class_id = ke.class_id
		WHERE ke.material_id = ? AND ke.class_id = ?
		GROUP BY ke.id, ke.chunk_strategy`, materialID, classID)

	var (
		stats                ChunkStats
		failedRows, pendRows int
	)
	if err := row.Scan(&stats.Strategy, &stats.Count, &failedRows, &pendRows); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ChunkStats{}, ErrNotFound
		}
		return ChunkStats{}, fmt.Errorf("chunk stats: %w", err)
	}

	switch {
	case failedRows > 0:
		stats.Status = IndexFailed
	case pendRows > 0 || stats.Count == 0:
		// No chunks yet means the entry still needs its first chunking pass, so
		// reporting pending is the honest state until the compensation scan or
		// a reindex runs.
		stats.Status = IndexPending
	default:
		stats.Status = IndexIndexed
	}
	if stats.Strategy == "" {
		stats.Strategy = "auto"
	}
	return stats, nil
}

// UpdateChunksStatus moves every chunk in ids to the given status.
func UpdateChunksStatus(ctx context.Context, conn *sql.DB, ids []int64, status IndexStatus) error {
	return updateChunksStatus(ctx, conn, ids, status)
}

func updateChunksStatus(ctx context.Context, ex sqlExecutor, ids []int64, status IndexStatus) error {
	if len(ids) == 0 {
		return nil
	}

	args := make([]any, 0, len(ids)+1)
	args = append(args, status)
	for _, id := range ids {
		args = append(args, id)
	}
	query := `UPDATE knowledge_chunks SET index_status = ? WHERE id IN (` + placeholders(len(ids)) + `)`
	if _, err := ex.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("update chunk status: %w", err)
	}
	return nil
}

// ReplaceChunksForMaterial swaps a material's chunks inside one transaction:
// the old rows go, the new rows arrive as pending, and the knowledge entry
// records the strategy that produced them (design.md Decision 7).
//
// MaterialID, ClassID and IndexStatus on the supplied chunks are overridden
// from the arguments, so a caller cannot accidentally write a chunk into
// another class.
func ReplaceChunksForMaterial(ctx context.Context, conn *sql.DB, materialID, classID int, strategy string, params []byte, chunks []Chunk) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM knowledge_chunks WHERE material_id = ? AND class_id = ?`, materialID, classID); err != nil {
		return fmt.Errorf("delete old chunks: %w", err)
	}

	// Resolve the knowledge entry the new chunks belong to. The lookup doubles
	// as the "material is not in this class" check, which must be indistinguishable
	// from "material does not exist".
	var entryID int
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM knowledge_entries WHERE material_id = ? AND class_id = ?`,
		materialID, classID).Scan(&entryID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("look up knowledge entry: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE knowledge_entries SET chunk_strategy = ?, chunk_params = ? WHERE id = ?`,
		strategy, params, entryID); err != nil {
		return fmt.Errorf("update chunk strategy: %w", err)
	}

	for i := range chunks {
		chunks[i].KnowledgeEntryID = entryID
		chunks[i].MaterialID = materialID
		chunks[i].ClassID = classID
		chunks[i].IndexStatus = IndexPending
	}
	if err := insertChunks(ctx, tx, chunks); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace tx: %w", err)
	}
	return nil
}

// placeholders renders "?, ?, ?" for an IN clause.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func scanChunk(rows *sql.Rows) (Chunk, error) {
	var c Chunk
	if err := rows.Scan(&c.ID, &c.KnowledgeEntryID, &c.MaterialID, &c.ClassID, &c.ChunkIndex,
		&c.CharStart, &c.CharEnd, &c.ChunkText, &c.IndexStatus, &c.CreatedAt); err != nil {
		return Chunk{}, fmt.Errorf("scan chunk: %w", err)
	}
	return c, nil
}
