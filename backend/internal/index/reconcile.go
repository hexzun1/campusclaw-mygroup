package index

import (
	"context"
	"database/sql"
	"log"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/knowledge"
)

// ReconcileStore is the slice of internal/db the startup compensation scan
// needs, over and above what the indexer itself uses.
type ReconcileStore interface {
	ListEntriesWithoutChunks(ctx context.Context, afterID, limit int) ([]db.KnowledgeEntry, error)
	ListMaterialsWithPendingChunks(ctx context.Context, afterMaterialID, limit int) ([]db.MaterialRef, error)
	InsertChunks(ctx context.Context, chunks []db.Chunk) error
}

// SQLReconcileStore adapts *sql.DB to ReconcileStore.
type SQLReconcileStore struct {
	Conn *sql.DB
}

// ListEntriesWithoutChunks delegates to internal/db.
func (s SQLReconcileStore) ListEntriesWithoutChunks(ctx context.Context, afterID, limit int) ([]db.KnowledgeEntry, error) {
	return db.ListEntriesWithoutChunks(ctx, s.Conn, afterID, limit)
}

// ListMaterialsWithPendingChunks delegates to internal/db.
func (s SQLReconcileStore) ListMaterialsWithPendingChunks(ctx context.Context, afterMaterialID, limit int) ([]db.MaterialRef, error) {
	return db.ListMaterialsWithPendingChunks(ctx, s.Conn, afterMaterialID, limit)
}

// InsertChunks delegates to internal/db.
func (s SQLReconcileStore) InsertChunks(ctx context.Context, chunks []db.Chunk) error {
	return db.InsertChunks(ctx, s.Conn, chunks)
}

// reconcileBatchSize bounds one page of the scan.
const reconcileBatchSize = 50

// Reconcile is the startup compensation scan (design.md Decision 6).
//
// It does two things: knowledge entries that have no chunks at all — iteration-1
// and seeded materials — get their chunks generated with the auto strategy, and
// materials with chunks still marked pending get their indexing finished. Both
// steps are idempotent: after a pass an entry has chunks and a pending chunk is
// either indexed or failed, so a second pass changes nothing.
//
// Chunks marked failed are deliberately left alone; those need a teacher to
// reindex the material.
//
// A dependency being unavailable is not fatal: the indexer records it on the
// chunks and the scan carries on, so the api still starts with Qdrant or a
// gateway down.
func Reconcile(ctx context.Context, store ReconcileStore, indexer *Indexer) error {
	if err := reconcileUnchunked(ctx, store, indexer); err != nil {
		return err
	}
	return reconcilePending(ctx, store, indexer)
}

// reconcileUnchunked chunks and indexes every knowledge entry that has no
// chunks yet.
func reconcileUnchunked(ctx context.Context, store ReconcileStore, indexer *Indexer) error {
	// The auto strategy is what the design prescribes here, and it is also what
	// every pre-existing row is recorded as.
	params := knowledge.DefaultParams()

	// The cursor advances whatever happens to an entry, so the scan always
	// terminates even if a body produces no chunk at all.
	for cursor := 0; ; {
		entries, err := store.ListEntriesWithoutChunks(ctx, cursor, reconcileBatchSize)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return nil
		}

		for _, entry := range entries {
			cursor = entry.ID
			chunks := toDBChunksForEntry(entry, params)
			if len(chunks) == 0 {
				// A body that preprocesses to nothing produces no chunk: there is
				// nothing to index and nothing to retry.
				log.Printf("reconcile: entry %d produced no chunks", entry.ID)
				continue
			}
			if err := store.InsertChunks(ctx, chunks); err != nil {
				return err
			}
			log.Printf("reconcile: generated %d chunks for entry %d", len(chunks), entry.ID)
			if err := indexer.IndexMaterial(ctx, entry.MaterialID, entry.ClassID); err != nil {
				log.Printf("reconcile: index material %d: %v", entry.MaterialID, err)
			}
		}
	}
}

// reconcilePending finishes interrupted indexing runs.
func reconcilePending(ctx context.Context, store ReconcileStore, indexer *Indexer) error {
	for cursor := 0; ; {
		refs, err := store.ListMaterialsWithPendingChunks(ctx, cursor, reconcileBatchSize)
		if err != nil {
			return err
		}
		if len(refs) == 0 {
			return nil
		}

		for _, ref := range refs {
			cursor = ref.MaterialID
			if err := indexer.IndexMaterial(ctx, ref.MaterialID, ref.ClassID); err != nil {
				log.Printf("reconcile: index material %d: %v", ref.MaterialID, err)
			}
		}
	}
}

// toDBChunksForEntry turns the auto chunking of one entry into storage rows.
func toDBChunksForEntry(entry db.KnowledgeEntry, params knowledge.ChunkParams) []db.Chunk {
	produced := knowledge.ChunkBody(entry.BodyText, params)
	chunks := make([]db.Chunk, 0, len(produced))
	for _, c := range produced {
		chunks = append(chunks, db.Chunk{
			KnowledgeEntryID: entry.ID,
			MaterialID:       entry.MaterialID,
			ClassID:          entry.ClassID,
			ChunkIndex:       c.Index,
			CharStart:        c.Span.Start,
			CharEnd:          c.Span.End,
			ChunkText:        c.Text,
			IndexStatus:      db.IndexPending,
		})
	}
	return chunks
}
