package index

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"campusclaw/backend/internal/db"
)

// fakeReconcileStore is an in-memory stand-in for the compensation scan's view
// of the database. It implements both ChunkStore and ReconcileStore, and it
// derives "entries without chunks" from the chunks it actually holds, the same
// way the SQL does.
type fakeReconcileStore struct {
	mu sync.Mutex

	entries []db.KnowledgeEntry
	chunks  []db.Chunk
	nextID  int64

	insertCalls  int
	updateErrors int
}

func (f *fakeReconcileStore) ListEntriesWithoutChunks(_ context.Context, afterID, limit int) ([]db.KnowledgeEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []db.KnowledgeEntry
	for _, e := range f.entries {
		if e.ID <= afterID || f.hasChunksFor(e.ID) {
			continue
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// hasChunksFor reports whether an entry already has rows, which the caller
// holds the lock for.
func (f *fakeReconcileStore) hasChunksFor(entryID int) bool {
	for _, c := range f.chunks {
		if c.KnowledgeEntryID == entryID {
			return true
		}
	}
	return false
}

func (f *fakeReconcileStore) ListMaterialsWithPendingChunks(_ context.Context, afterMaterialID, limit int) ([]db.MaterialRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	seen := map[int]bool{}
	var out []db.MaterialRef
	for _, c := range f.chunks {
		if c.IndexStatus != db.IndexPending || c.MaterialID <= afterMaterialID || seen[c.MaterialID] {
			continue
		}
		seen[c.MaterialID] = true
		out = append(out, db.MaterialRef{MaterialID: c.MaterialID, ClassID: c.ClassID})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeReconcileStore) InsertChunks(_ context.Context, chunks []db.Chunk) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.insertCalls++
	for _, c := range chunks {
		c.ID = f.nextID
		f.nextID++
		f.chunks = append(f.chunks, c)
	}
	return nil
}

func (f *fakeReconcileStore) ListChunksByMaterial(_ context.Context, materialID, classID int, status db.IndexStatus) ([]db.Chunk, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

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

func (f *fakeReconcileStore) UpdateChunksStatus(_ context.Context, ids []int64, status db.IndexStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, id := range ids {
		for i := range f.chunks {
			if f.chunks[i].ID == id {
				f.chunks[i].IndexStatus = status
			}
		}
	}
	return nil
}

func (f *fakeReconcileStore) chunkCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.chunks)
}

func (f *fakeReconcileStore) statuses() []db.IndexStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]db.IndexStatus, 0, len(f.chunks))
	for _, c := range f.chunks {
		out = append(out, c.IndexStatus)
	}
	return out
}

// entryFor builds an entry whose body will chunk into one window.
func entryFor(id, materialID, classID int, body string) db.KnowledgeEntry {
	return db.KnowledgeEntry{
		ID: id, MaterialID: materialID, ClassID: classID, BodyText: body,
	}
}

func runReconcile(t *testing.T, store *fakeReconcileStore, embedder *fakeEmbedder, vectors *fakeVectors) {
	t.Helper()
	indexer := New(store, embedder, vectors, 32, 5*time.Second)
	if err := Reconcile(context.Background(), store, indexer); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
}

func TestReconcileChunksAndIndexesEntriesWithoutChunks(t *testing.T) {
	store := &fakeReconcileStore{entries: []db.KnowledgeEntry{
		entryFor(1, 11, 1, "第一份遗留材料的正文，足够长到产生切片。"),
		entryFor(2, 12, 1, "第二份遗留材料的正文，同样足够长到产生切片。"),
	}}
	embedder := &fakeEmbedder{dim: 4}
	vectors := &fakeVectors{}

	runReconcile(t, store, embedder, vectors)

	if got := store.chunkCount(); got != 2 {
		t.Fatalf("generated %d chunks, want 2", got)
	}
	for i, status := range store.statuses() {
		if status != db.IndexIndexed {
			t.Fatalf("chunk %d status = %q, want indexed", i, status)
		}
	}
	if got := vectors.totalPoints(); got != 2 {
		t.Fatalf("wrote %d points, want 2", got)
	}
}

func TestReconcileFinishesInterruptedIndexing(t *testing.T) {
	store := &fakeReconcileStore{}
	if err := store.InsertChunks(context.Background(), []db.Chunk{
		{KnowledgeEntryID: 1, MaterialID: 11, ClassID: 1, ChunkIndex: 0, ChunkText: "未完成的切片", IndexStatus: db.IndexPending},
		{KnowledgeEntryID: 1, MaterialID: 11, ClassID: 1, ChunkIndex: 1, ChunkText: "另一片", IndexStatus: db.IndexPending},
	}); err != nil {
		t.Fatalf("seed chunks: %v", err)
	}
	embedder := &fakeEmbedder{dim: 4}
	vectors := &fakeVectors{}

	runReconcile(t, store, embedder, vectors)

	for i, status := range store.statuses() {
		if status != db.IndexIndexed {
			t.Fatalf("chunk %d status = %q, want indexed", i, status)
		}
	}
	if got := vectors.totalPoints(); got != 2 {
		t.Fatalf("wrote %d points, want 2", got)
	}
}

// TestReconcileIsIdempotent is the "连续重启两次后切片总数不变" rule.
func TestReconcileIsIdempotent(t *testing.T) {
	store := &fakeReconcileStore{entries: []db.KnowledgeEntry{
		entryFor(1, 11, 1, "正文内容足够长，能够产生至少一个切片。"),
	}}
	embedder := &fakeEmbedder{dim: 4}
	vectors := &fakeVectors{}

	runReconcile(t, store, embedder, vectors)
	afterFirst := store.chunkCount()
	if afterFirst == 0 {
		t.Fatal("the first pass generated no chunks")
	}

	runReconcile(t, store, embedder, vectors)
	runReconcile(t, store, embedder, vectors)

	if got := store.chunkCount(); got != afterFirst {
		t.Fatalf("chunk count changed from %d to %d after repeated passes", afterFirst, got)
	}
	if got := vectors.totalPoints(); got != afterFirst {
		t.Fatalf("vector count is %d, want %d", got, afterFirst)
	}
}

// TestReconcileTerminatesWhenAnEntryProducesNoChunk guards the cursor: an entry
// whose body preprocesses to nothing stays unchunked, and the scan must still
// finish instead of paging over it forever.
func TestReconcileTerminatesWhenAnEntryProducesNoChunk(t *testing.T) {
	store := &fakeReconcileStore{entries: []db.KnowledgeEntry{
		entryFor(1, 11, 1, "   "),
		entryFor(2, 12, 1, "后面这份正常正文能产生切片。"),
	}}
	embedder := &fakeEmbedder{dim: 4}
	vectors := &fakeVectors{}

	done := make(chan struct{})
	go func() {
		defer close(done)
		runReconcile(t, store, embedder, vectors)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Reconcile did not terminate on an entry with no possible chunk")
	}

	if got := store.chunkCount(); got != 1 {
		t.Fatalf("generated %d chunks, want 1 (only the normal body)", got)
	}
}

func TestReconcileLeavesFailedChunksAlone(t *testing.T) {
	store := &fakeReconcileStore{}
	if err := store.InsertChunks(context.Background(), []db.Chunk{
		{KnowledgeEntryID: 1, MaterialID: 11, ClassID: 1, ChunkIndex: 0, ChunkText: "失败的切片", IndexStatus: db.IndexFailed},
	}); err != nil {
		t.Fatalf("seed chunks: %v", err)
	}
	embedder := &fakeEmbedder{dim: 4}
	vectors := &fakeVectors{}

	runReconcile(t, store, embedder, vectors)

	if got := store.statuses()[0]; got != db.IndexFailed {
		t.Fatalf("a failed chunk was retried: status = %q", got)
	}
	if embedder.callCount() != 0 {
		t.Fatalf("the embedder was called %d times, want none", embedder.callCount())
	}
}

// TestReconcileSurvivesDependencyFailure covers "索引依赖不可用时种子仍完成":
// the scan finishes and the chunks record the failure.
func TestReconcileSurvivesDependencyFailure(t *testing.T) {
	store := &fakeReconcileStore{entries: []db.KnowledgeEntry{
		entryFor(1, 11, 1, "依赖不可用时这份正文依然要被切片。"),
	}}
	embedder := &fakeEmbedder{dim: 4, failAt: 1}
	vectors := &fakeVectors{}

	runReconcile(t, store, embedder, vectors)

	if got := store.chunkCount(); got != 1 {
		t.Fatalf("generated %d chunks, want 1 even though embedding failed", got)
	}
	if got := store.statuses()[0]; got != db.IndexFailed {
		t.Fatalf("chunk status = %q, want failed", got)
	}
}

func TestReconcileGeneratesAutoWindowsForLegacyBodies(t *testing.T) {
	body := strings.Repeat("字", 2000)
	store := &fakeReconcileStore{entries: []db.KnowledgeEntry{entryFor(1, 11, 1, body)}}
	embedder := &fakeEmbedder{dim: 4}
	vectors := &fakeVectors{}

	runReconcile(t, store, embedder, vectors)

	chunks, err := store.ListChunksByMaterial(context.Background(), 11, 1, "")
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("generated %d chunks, want the three auto windows", len(chunks))
	}
	for i, wantSpan := range [][2]int{{0, 800}, {720, 1520}, {1440, 2000}} {
		if chunks[i].CharStart != wantSpan[0] || chunks[i].CharEnd != wantSpan[1] {
			t.Fatalf("chunk %d spans [%d,%d), want [%d,%d)",
				i, chunks[i].CharStart, chunks[i].CharEnd, wantSpan[0], wantSpan[1])
		}
		if chunks[i].KnowledgeEntryID != 1 || chunks[i].ClassID != 1 {
			t.Fatalf("chunk %d is not attached to the entry/class: %+v", i, chunks[i])
		}
	}
}
