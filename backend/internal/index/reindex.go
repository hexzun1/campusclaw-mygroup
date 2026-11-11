package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/knowledge"
)

// ErrReindexNotFound reports that the material's knowledge entry disappeared
// or no longer belongs to the requested class between the handler's lookup and
// the rebuild's critical section.
var ErrReindexNotFound = errors.New("reindex target not found")

// ReindexStore is the slice of internal/db the full rebuild needs. It embeds
// ChunkStore because the failure-marking path lists and updates the material's
// chunks through the same store.
type ReindexStore interface {
	ChunkStore

	GetKnowledgeEntryByMaterialID(ctx context.Context, materialID int) (*db.KnowledgeEntry, error)
	ReplaceChunksForMaterial(ctx context.Context, materialID, classID int, strategy string, params []byte, chunks []db.Chunk) error
	GetChunkStats(ctx context.Context, materialID, classID int) (db.ChunkStats, error)
}

// SQLReindexStore adapts *sql.DB to ReindexStore.
type SQLReindexStore struct {
	Conn *sql.DB
}

// GetKnowledgeEntryByMaterialID delegates to internal/db.
func (s SQLReindexStore) GetKnowledgeEntryByMaterialID(ctx context.Context, materialID int) (*db.KnowledgeEntry, error) {
	return db.GetKnowledgeEntryByMaterialID(ctx, s.Conn, materialID)
}

// ReplaceChunksForMaterial delegates to internal/db.
func (s SQLReindexStore) ReplaceChunksForMaterial(ctx context.Context, materialID, classID int, strategy string, params []byte, chunks []db.Chunk) error {
	return db.ReplaceChunksForMaterial(ctx, s.Conn, materialID, classID, strategy, params, chunks)
}

// GetChunkStats delegates to internal/db.
func (s SQLReindexStore) GetChunkStats(ctx context.Context, materialID, classID int) (db.ChunkStats, error) {
	return db.GetChunkStats(ctx, s.Conn, materialID, classID)
}

// ListChunksByMaterial delegates to internal/db.
func (s SQLReindexStore) ListChunksByMaterial(ctx context.Context, materialID, classID int, status db.IndexStatus) ([]db.Chunk, error) {
	return db.ListChunksByMaterial(ctx, s.Conn, materialID, classID, status)
}

// UpdateChunksStatus delegates to internal/db.
func (s SQLReindexStore) UpdateChunksStatus(ctx context.Context, ids []int64, status db.IndexStatus) error {
	return db.UpdateChunksStatus(ctx, s.Conn, ids, status)
}

// VectorDeleter is the slice of the vector store a rebuild needs before it can
// swap the chunks.
type VectorDeleter interface {
	DeleteByMaterial(ctx context.Context, materialID, classID int) error
}

// ReindexRequest describes one full rebuild. The handler validates role, class
// and chunking parameters before handing it over; MaterialID and ClassID come
// from the server's own lookup, never from the request body.
type ReindexRequest struct {
	MaterialID int
	ClassID    int
	Params     knowledge.ChunkParams
	Store      ReindexStore
	Vectors    VectorDeleter
}

// Reindex rebuilds one material's chunks under a new chunking strategy
// (spec: 索引状态与重建, design D3).
//
// It is the external entry point for a rebuild, so it acquires the same
// per-material lock IndexMaterial uses and holds it across the entire
// destructive and rebuilding lifecycle: re-reading the material identity,
// deleting the old vectors, replacing the chunks in one transaction, indexing
// the new chunks (including failure marking), and reading the stats snapshot
// returned to the caller. The response itself is written by the caller after
// this method has returned and the lock has been released.
//
// The order is deliberate: the vectors go first, then the chunks are swapped,
// then the new chunks are indexed. Deleting first means no point is ever left
// pointing at a chunk that has been replaced. A vector-store failure leaves
// MySQL completely untouched; a failure of the replacement transaction leaves
// the old chunks in place and marks them failed (best effort, still inside the
// critical section), because their vectors are already gone.
//
// Cancellation is checked again after the lock is acquired: a request that was
// canceled while waiting must not start any destructive work.
func (i *Indexer) Reindex(ctx context.Context, req ReindexRequest) (db.ChunkStats, error) {
	unlock := i.locks.lock(req.MaterialID)
	defer unlock()

	if err := ctx.Err(); err != nil {
		return db.ChunkStats{}, err
	}
	if req.Store == nil {
		return db.ChunkStats{}, errors.New("reindex: no store supplied")
	}

	// Re-read the identity under the lock: the material may have been deleted
	// or moved after the handler's lookup, and the body read here is the input
	// this rebuild actually uses.
	entry, err := req.Store.GetKnowledgeEntryByMaterialID(ctx, req.MaterialID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return db.ChunkStats{}, ErrReindexNotFound
		}
		return db.ChunkStats{}, fmt.Errorf("reindex material %d: read knowledge entry: %w", req.MaterialID, err)
	}
	if entry.ClassID != req.ClassID {
		return db.ChunkStats{}, ErrReindexNotFound
	}

	// 1. The old vectors go first: nothing may point at chunks that are about
	// to be replaced. On failure MySQL is left completely untouched.
	if req.Vectors != nil {
		if err := req.Vectors.DeleteByMaterial(ctx, req.MaterialID, req.ClassID); err != nil {
			log.Printf("reindex material %d: delete vectors: %v", req.MaterialID, err)
			return db.ChunkStats{}, fmt.Errorf("reindex material %d: delete vectors: %w", req.MaterialID, err)
		}
	}

	// 2. Swap the chunks in one transaction. Short transaction: it contains no
	// vector-store network call.
	chunks := toDBChunks(knowledge.ChunkBody(entry.BodyText, req.Params))
	if err := req.Store.ReplaceChunksForMaterial(ctx, req.MaterialID, req.ClassID,
		string(req.Params.Strategy), req.Params.ParamsJSON(), chunks); err != nil {
		log.Printf("reindex material %d: replace chunks: %v", req.MaterialID, err)
		// The vectors are gone and the old chunks are still here, so the honest
		// state is "failed"; a teacher can reindex again. Best effort, bounded,
		// and completed before the lock is released.
		i.markChunksFailed(ctx, req.Store, req.MaterialID, req.ClassID)
		return db.ChunkStats{}, fmt.Errorf("reindex material %d: replace chunks: %w", req.MaterialID, err)
	}

	// 3. Index the new chunks through the internal path: the material lock is
	// already held, and taking it again would deadlock. A dependency failure is
	// recorded on the chunks and does not turn the rebuild into an error.
	if err := i.indexMaterialLocked(ctx, req.Store, req.MaterialID, req.ClassID); err != nil {
		log.Printf("reindex material %d: index: %v", req.MaterialID, err)
	}

	// 4. Build the immutable stats snapshot while still inside the critical
	// section, so a following rebuild cannot mix its strategy or chunk count
	// into this response.
	stats, err := req.Store.GetChunkStats(ctx, req.MaterialID, req.ClassID)
	if err != nil {
		return db.ChunkStats{}, fmt.Errorf("reindex material %d: chunk stats: %w", req.MaterialID, err)
	}
	return stats, nil
}

// markChunksFailed reports the failure on every chunk of a material. It runs on
// a detached, bounded context so a canceled request still records the honest
// state, and it is called before the material lock is released.
func (i *Indexer) markChunksFailed(parent context.Context, store ReindexStore, materialID, classID int) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), failMarkTimeout)
	defer cancel()

	chunks, err := store.ListChunksByMaterial(ctx, materialID, classID, "")
	if err != nil {
		log.Printf("reindex material %d: list chunks for failure marking: %v", materialID, err)
		return
	}
	ids := make([]int64, 0, len(chunks))
	for _, c := range chunks {
		ids = append(ids, c.ID)
	}
	if err := store.UpdateChunksStatus(ctx, ids, db.IndexFailed); err != nil {
		log.Printf("reindex material %d: mark %d chunks failed: %v", materialID, len(ids), err)
	}
}

// toDBChunks turns chunker output into storage rows. The identifiers, class,
// material and status are assigned by the database layer, so a caller cannot
// place a chunk in another class.
func toDBChunks(produced []knowledge.Chunk) []db.Chunk {
	chunks := make([]db.Chunk, 0, len(produced))
	for _, c := range produced {
		chunks = append(chunks, db.Chunk{
			ChunkIndex: c.Index,
			CharStart:  c.Span.Start,
			CharEnd:    c.Span.End,
			ChunkText:  c.Text,
		})
	}
	return chunks
}
