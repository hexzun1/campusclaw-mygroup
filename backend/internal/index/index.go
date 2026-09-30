// Package index runs the vector-index pipeline: read a material's pending
// chunks, embed them in batches, write the vectors, and record what happened on
// each chunk (design.md Decision 6).
//
// Indexing is synchronous and its failures never roll back material rows: a
// dependency that is down leaves the material usable through keyword search and
// recoverable through a reindex.
package index

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"sync"
	"time"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/vector"
)

const (
	// DefaultBatchSize mirrors EMBEDDING_BATCH_SIZE.
	DefaultBatchSize = 32
	// DefaultTimeout mirrors INDEX_TIMEOUT_SECONDS.
	DefaultTimeout = 120 * time.Second
	// failMarkTimeout bounds the status write that records a failure. It runs on
	// a detached context, because the failure may well have been a timeout.
	failMarkTimeout = 10 * time.Second
)

// errVectorCount reports a batch whose embedding result cannot be matched to
// its chunks.
var errVectorCount = errors.New("the embedding gateway returned the wrong number of vectors for a batch")

// Embedder is the slice of the embedding gateway the indexer needs.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
}

// VectorStore is the slice of the vector store the indexer needs.
type VectorStore interface {
	Upsert(ctx context.Context, points []vector.Point) error
}

// ChunkStore is the slice of internal/db the indexer needs.
type ChunkStore interface {
	ListChunksByMaterial(ctx context.Context, materialID, classID int, status db.IndexStatus) ([]db.Chunk, error)
	UpdateChunksStatus(ctx context.Context, ids []int64, status db.IndexStatus) error
}

// SQLChunkStore adapts *sql.DB to ChunkStore.
type SQLChunkStore struct {
	Conn *sql.DB
}

// ListChunksByMaterial delegates to internal/db.
func (s SQLChunkStore) ListChunksByMaterial(ctx context.Context, materialID, classID int, status db.IndexStatus) ([]db.Chunk, error) {
	return db.ListChunksByMaterial(ctx, s.Conn, materialID, classID, status)
}

// UpdateChunksStatus delegates to internal/db.
func (s SQLChunkStore) UpdateChunksStatus(ctx context.Context, ids []int64, status db.IndexStatus) error {
	return db.UpdateChunksStatus(ctx, s.Conn, ids, status)
}

// Indexer drives the pipeline.
type Indexer struct {
	chunks   ChunkStore
	embedder Embedder
	vectors  VectorStore
	batch    int
	timeout  time.Duration
	locks    keyedMutex
}

// New builds an indexer. Non-positive batch size and timeout fall back to the
// documented defaults.
func New(chunks ChunkStore, embedder Embedder, vectors VectorStore, batch int, timeout time.Duration) *Indexer {
	if batch <= 0 {
		batch = DefaultBatchSize
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Indexer{
		chunks:   chunks,
		embedder: embedder,
		vectors:  vectors,
		batch:    batch,
		timeout:  timeout,
	}
}

// IndexMaterial indexes every pending chunk of one material.
//
// The work runs on a context detached from the caller's, with its own
// timeout: a client that disconnects mid-upload must not leave chunks pending
// forever. A failing batch marks itself and every chunk after it as failed and
// stops, so the recorded state always describes what really happened. The
// returned error is only for unexpected problems such as a broken database —
// dependency failures are recorded on the chunks and reported as nil.
func (i *Indexer) IndexMaterial(ctx context.Context, materialID, classID int) error {
	unlock := i.locks.lock(materialID)
	defer unlock()

	workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), i.timeout)
	defer cancel()

	pending, err := i.chunks.ListChunksByMaterial(workCtx, materialID, classID, db.IndexPending)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}

	for start := 0; start < len(pending); start += i.batch {
		batch := pending[start:min(start+i.batch, len(pending))]

		if err := i.indexBatch(workCtx, batch); err != nil {
			log.Printf("index material %d: batch of %d chunks failed: %v", materialID, len(batch), err)
			i.markFailed(ctx, pending[start:])
			return nil
		}
		if err := i.chunks.UpdateChunksStatus(workCtx, chunkIDs(batch), db.IndexIndexed); err != nil {
			return err
		}
	}
	return nil
}

// indexBatch embeds one batch and writes its vectors.
func (i *Indexer) indexBatch(ctx context.Context, batch []db.Chunk) error {
	texts := make([]string, len(batch))
	for j, c := range batch {
		texts[j] = c.ChunkText
	}

	vectors, err := i.embedder.Embed(ctx, texts)
	if err != nil {
		return err
	}
	if len(vectors) != len(batch) {
		return errVectorCount
	}

	points := make([]vector.Point, len(batch))
	for j, c := range batch {
		points[j] = vector.Point{
			ID:     c.ID,
			Vector: vectors[j],
			// The payload holds identifiers only: the body stays in MySQL, which
			// is the single source of truth for traceability.
			Payload: vector.PointPayload{
				ClassID:          c.ClassID,
				MaterialID:       c.MaterialID,
				KnowledgeEntryID: c.KnowledgeEntryID,
				ChunkID:          c.ID,
				ChunkIndex:       c.ChunkIndex,
			},
		}
	}
	return i.vectors.Upsert(ctx, points)
}

// markFailed records the failure on a detached context, so a timed-out batch
// can still be reported.
func (i *Indexer) markFailed(parent context.Context, chunks []db.Chunk) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), failMarkTimeout)
	defer cancel()

	if err := i.chunks.UpdateChunksStatus(ctx, chunkIDs(chunks), db.IndexFailed); err != nil {
		log.Printf("mark %d chunks failed: %v", len(chunks), err)
	}
}

func chunkIDs(chunks []db.Chunk) []int64 {
	ids := make([]int64, 0, len(chunks))
	for _, c := range chunks {
		ids = append(ids, c.ID)
	}
	return ids
}

// keyedMutex serialises work per material, so two uploads of the same material
// (or a reindex racing an upload) cannot interleave their batches. The design
// assumes a single instance, so an in-process lock is enough.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[int]*sync.Mutex
}

func (k *keyedMutex) lock(key int) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = map[int]*sync.Mutex{}
	}
	lock, ok := k.locks[key]
	if !ok {
		lock = &sync.Mutex{}
		k.locks[key] = lock
	}
	k.mu.Unlock()

	lock.Lock()
	return lock.Unlock
}
