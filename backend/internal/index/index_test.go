package index

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/vector"
)

// fakeChunks is an in-memory ChunkStore that records status transitions.
type fakeChunks struct {
	mu      sync.Mutex
	chunks  []db.Chunk
	updates []statusUpdate

	listErr   error
	updateErr error
}

type statusUpdate struct {
	ids    []int64
	status db.IndexStatus
}

func (f *fakeChunks) ListChunksByMaterial(_ context.Context, materialID, classID int, status db.IndexStatus) ([]db.Chunk, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []db.Chunk
	for _, c := range f.chunks {
		if c.MaterialID != materialID || c.ClassID != classID {
			continue
		}
		if status != "" && c.IndexStatus != status {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeChunks) UpdateChunksStatus(_ context.Context, ids []int64, status db.IndexStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updates = append(f.updates, statusUpdate{ids: ids, status: status})
	for i := range f.chunks {
		for _, id := range ids {
			if f.chunks[i].ID == id {
				f.chunks[i].IndexStatus = status
			}
		}
	}
	return nil
}

func (f *fakeChunks) statusOf(id int64) db.IndexStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.chunks {
		if c.ID == id {
			return c.IndexStatus
		}
	}
	return ""
}

func (f *fakeChunks) batchSizes() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	sizes := make([]int, 0, len(f.updates))
	for _, u := range f.updates {
		sizes = append(sizes, len(u.ids))
	}
	return sizes
}

// fakeEmbedder fails on demand and records the texts it was asked for.
type fakeEmbedder struct {
	mu sync.Mutex

	dim     int
	calls   [][]string
	failAt  int // 1-based call number that fails; 0 never fails
	callNum int
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callNum++
	f.calls = append(f.calls, texts)
	if f.failAt != 0 && f.callNum == f.failAt {
		return nil, errors.New("embedding dependency is down")
	}
	out := make([][]float64, len(texts))
	for i := range out {
		out[i] = make([]float64, f.dim)
		out[i][0] = 1
	}
	return out, nil
}

func (f *fakeEmbedder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeVectors records upserts and can fail on demand.
type fakeVectors struct {
	mu sync.Mutex

	points [][]vector.Point
	failAt int
	callNo int
}

func (f *fakeVectors) Upsert(_ context.Context, points []vector.Point) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callNo++
	f.points = append(f.points, points)
	if f.failAt != 0 && f.callNo == f.failAt {
		return errors.New("vector store is down")
	}
	return nil
}

func (f *fakeVectors) totalPoints() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, batch := range f.points {
		total += len(batch)
	}
	return total
}

func pendingChunks(materialID, classID, count int) []db.Chunk {
	chunks := make([]db.Chunk, 0, count)
	for i := 0; i < count; i++ {
		chunks = append(chunks, db.Chunk{
			ID:               int64(1000 + materialID*100 + i),
			KnowledgeEntryID: materialID,
			MaterialID:       materialID,
			ClassID:          classID,
			ChunkIndex:       i,
			CharStart:        i * 10,
			CharEnd:          i*10 + 10,
			ChunkText:        "chunk text",
			IndexStatus:      db.IndexPending,
		})
	}
	return chunks
}

func newTestIndexer(chunks *fakeChunks, embedder *fakeEmbedder, vectors *fakeVectors, batch int) *Indexer {
	return New(chunks, embedder, vectors, batch, 5*time.Second)
}

func TestIndexMaterialMarksEveryChunkIndexed(t *testing.T) {
	chunks := &fakeChunks{chunks: pendingChunks(1, 7, 3)}
	embedder := &fakeEmbedder{dim: 4}
	vectors := &fakeVectors{}

	if err := newTestIndexer(chunks, embedder, vectors, 32).IndexMaterial(context.Background(), 1, 7); err != nil {
		t.Fatalf("IndexMaterial failed: %v", err)
	}

	for _, c := range pendingChunks(1, 7, 3) {
		if got := chunks.statusOf(c.ID); got != db.IndexIndexed {
			t.Fatalf("chunk %d status = %q, want indexed", c.ID, got)
		}
	}
	if got := vectors.totalPoints(); got != 3 {
		t.Fatalf("wrote %d points, want 3", got)
	}
}

func TestIndexMaterialSendsChunkTextAndIdentifierPayload(t *testing.T) {
	chunks := &fakeChunks{chunks: []db.Chunk{{
		ID: 5, KnowledgeEntryID: 6, MaterialID: 1, ClassID: 7, ChunkIndex: 2,
		ChunkText: "正文片段", IndexStatus: db.IndexPending,
	}}}
	embedder := &fakeEmbedder{dim: 3}
	vectors := &fakeVectors{}

	if err := newTestIndexer(chunks, embedder, vectors, 32).IndexMaterial(context.Background(), 1, 7); err != nil {
		t.Fatalf("IndexMaterial failed: %v", err)
	}

	if got := embedder.calls[0][0]; got != "正文片段" {
		t.Fatalf("embedded %q, want the chunk text", got)
	}
	point := vectors.points[0][0]
	if point.ID != 5 {
		t.Fatalf("point ID = %d, want the chunk id", point.ID)
	}
	want := vector.PointPayload{ClassID: 7, MaterialID: 1, KnowledgeEntryID: 6, ChunkID: 5, ChunkIndex: 2}
	if point.Payload != want {
		t.Fatalf("payload = %+v, want %+v", point.Payload, want)
	}
}

// TestIndexMaterialStopsAtTheFailingBatch is the core rule from tasks.md 5.1:
// the failing batch and everything after it becomes failed, earlier batches
// stay indexed.
func TestIndexMaterialStopsAtTheFailingBatch(t *testing.T) {
	chunks := &fakeChunks{chunks: pendingChunks(2, 7, 5)}
	embedder := &fakeEmbedder{dim: 4, failAt: 2}
	vectors := &fakeVectors{}

	if err := newTestIndexer(chunks, embedder, vectors, 2).IndexMaterial(context.Background(), 2, 7); err != nil {
		t.Fatalf("IndexMaterial returned %v, want nil (dependency failures are recorded, not returned)", err)
	}

	all := pendingChunks(2, 7, 5)
	for i, c := range all {
		want := db.IndexFailed
		if i < 2 {
			want = db.IndexIndexed
		}
		if got := chunks.statusOf(c.ID); got != want {
			t.Fatalf("chunk %d (batch %d) status = %q, want %q", i, i/2, got, want)
		}
	}

	// Nothing after the failure is sent anywhere.
	if got := embedder.callCount(); got != 2 {
		t.Fatalf("embedder was called %d times, want 2 (the second one failed)", got)
	}
	if got := vectors.totalPoints(); got != 2 {
		t.Fatalf("wrote %d points, want only the first batch", got)
	}
}

func TestIndexMaterialSplitsIntoBatches(t *testing.T) {
	chunks := &fakeChunks{chunks: pendingChunks(3, 7, 5)}
	embedder := &fakeEmbedder{dim: 2}
	vectors := &fakeVectors{}

	if err := newTestIndexer(chunks, embedder, vectors, 2).IndexMaterial(context.Background(), 3, 7); err != nil {
		t.Fatalf("IndexMaterial failed: %v", err)
	}

	if got := embedder.callCount(); got != 3 {
		t.Fatalf("embedder called %d times, want 3 batches (2+2+1)", got)
	}
	if got := chunks.batchSizes(); len(got) != 3 || got[0] != 2 || got[1] != 2 || got[2] != 1 {
		t.Fatalf("batches marked indexed = %v, want 2/2/1", got)
	}
}

func TestIndexMaterialFailsWhenVectorWriteFails(t *testing.T) {
	chunks := &fakeChunks{chunks: pendingChunks(4, 7, 3)}
	embedder := &fakeEmbedder{dim: 4}
	vectors := &fakeVectors{failAt: 1}

	if err := newTestIndexer(chunks, embedder, vectors, 32).IndexMaterial(context.Background(), 4, 7); err != nil {
		t.Fatalf("IndexMaterial returned %v, want nil", err)
	}

	for _, c := range pendingChunks(4, 7, 3) {
		if got := chunks.statusOf(c.ID); got != db.IndexFailed {
			t.Fatalf("chunk %d status = %q, want failed", c.ID, got)
		}
	}
}

func TestIndexMaterialWithNothingPendingDoesNothing(t *testing.T) {
	chunks := &fakeChunks{}
	embedder := &fakeEmbedder{dim: 4}
	vectors := &fakeVectors{}

	if err := newTestIndexer(chunks, embedder, vectors, 32).IndexMaterial(context.Background(), 9, 7); err != nil {
		t.Fatalf("IndexMaterial failed: %v", err)
	}
	if embedder.callCount() != 0 || vectors.totalPoints() != 0 {
		t.Fatal("an empty material still called a dependency")
	}
}

func TestIndexMaterialOnlyTouchesPendingChunks(t *testing.T) {
	all := pendingChunks(5, 7, 3)
	all[0].IndexStatus = db.IndexIndexed
	all[1].IndexStatus = db.IndexFailed
	chunks := &fakeChunks{chunks: all}
	embedder := &fakeEmbedder{dim: 4}
	vectors := &fakeVectors{}

	if err := newTestIndexer(chunks, embedder, vectors, 32).IndexMaterial(context.Background(), 5, 7); err != nil {
		t.Fatalf("IndexMaterial failed: %v", err)
	}

	if got := embedder.callCount(); got != 1 {
		t.Fatalf("embedder called %d times, want 1 (only the pending chunk)", got)
	}
	if got := vectors.totalPoints(); got != 1 {
		t.Fatalf("wrote %d points, want 1", got)
	}
	if got := chunks.statusOf(all[1].ID); got != db.IndexFailed {
		t.Fatalf("a failed chunk was retried: status = %q", got)
	}
}

func TestIndexMaterialPropagatesListErrors(t *testing.T) {
	chunks := &fakeChunks{listErr: errors.New("db is down")}
	indexer := newTestIndexer(chunks, &fakeEmbedder{dim: 4}, &fakeVectors{}, 32)

	if err := indexer.IndexMaterial(context.Background(), 1, 7); err == nil {
		t.Fatal("IndexMaterial hid a database failure")
	}
}

// TestIndexMaterialSurvivesCallerCancellation checks that the work is detached
// from the request context: a client that disconnects must not leave chunks
// pending.
func TestIndexMaterialSurvivesCallerCancellation(t *testing.T) {
	chunks := &fakeChunks{chunks: pendingChunks(6, 7, 3)}
	embedder := &fakeEmbedder{dim: 4}
	vectors := &fakeVectors{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := newTestIndexer(chunks, embedder, vectors, 32).IndexMaterial(ctx, 6, 7); err != nil {
		t.Fatalf("IndexMaterial failed: %v", err)
	}
	for _, c := range pendingChunks(6, 7, 3) {
		if got := chunks.statusOf(c.ID); got != db.IndexIndexed {
			t.Fatalf("chunk %d status = %q, want indexed despite the cancelled caller context", c.ID, got)
		}
	}
}

func TestKeyedMutexSerialisesTheSameKey(t *testing.T) {
	var locks keyedMutex
	unlockFirst := locks.lock(1)

	acquired := make(chan struct{})
	go func() {
		unlockSecond := locks.lock(1)
		defer unlockSecond()
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("the second lock on the same key was granted while the first was held")
	case <-time.After(50 * time.Millisecond):
	}

	unlockFirst()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("the second lock was never granted after the first was released")
	}
}

func TestKeyedMutexAllowsDifferentKeys(t *testing.T) {
	var locks keyedMutex
	unlockFirst := locks.lock(1)
	defer unlockFirst()

	acquired := make(chan struct{})
	go func() {
		unlockSecond := locks.lock(2)
		defer unlockSecond()
		close(acquired)
	}()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("locking another material blocked")
	}
}
