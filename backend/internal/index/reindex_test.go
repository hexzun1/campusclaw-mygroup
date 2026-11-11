package index

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/knowledge"
	"campusclaw/backend/internal/vector"
)

// The tests in this file pin the full rebuild critical section (design D3,
// tasks 3.1-3.3). Interleavings are fixed with barriers (channels and gates);
// the short observation windows only detect unexpected progress within that
// fixed window, never establish ordering by themselves. Final states and the
// operation log are the actual assertions.

// reindexStore is an in-memory ReindexStore with controllable gates.
type reindexStore struct {
	mu sync.Mutex

	entries map[int]*db.KnowledgeEntry
	chunks  []db.Chunk
	nextID  int64

	// strategies records the chunking strategy each material's last
	// replacement succeeded with.
	strategies map[int]string

	replaceCalls   int
	replaceFailAt  int // 1-based call that fails; 0 never
	replaceGate    <-chan struct{}
	replaceEntered chan struct{}
	replaceOnce    sync.Once
	// replaceGateMaterial, when non-zero, restricts the gate to one material so
	// unrelated materials can still proceed.
	replaceGateMaterial int

	markCalls   int
	markGate    <-chan struct{}
	markEntered chan struct{}
	markOnce    sync.Once

	statsErr error

	opLog []string
}

func newReindexStore(materialID, classID int, body string) *reindexStore {
	return &reindexStore{
		entries: map[int]*db.KnowledgeEntry{
			materialID: {ID: materialID, MaterialID: materialID, ClassID: classID, BodyText: body},
		},
		strategies: map[int]string{},
		nextID:     10_000,
	}
}

func (s *reindexStore) addEntry(materialID, classID int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[materialID] = &db.KnowledgeEntry{ID: materialID, MaterialID: materialID, ClassID: classID, BodyText: body}
}

// seedPending adds count pending chunks to the material.
func (s *reindexStore) seedPending(materialID, classID, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < count; i++ {
		s.chunks = append(s.chunks, db.Chunk{
			ID:               s.nextID,
			KnowledgeEntryID: materialID,
			MaterialID:       materialID,
			ClassID:          classID,
			ChunkIndex:       i,
			ChunkText:        "旧切片 " + fmt.Sprint(i),
			IndexStatus:      db.IndexPending,
		})
		s.nextID++
	}
}

func (s *reindexStore) recordLocked(op string) {
	s.opLog = append(s.opLog, op)
}

func (s *reindexStore) opCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.opLog)
}

func (s *reindexStore) opIndex(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, op := range s.opLog {
		if strings.HasPrefix(op, prefix) {
			return i
		}
	}
	return -1
}

func (s *reindexStore) replaceCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replaceCalls
}

func (s *reindexStore) markCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.markCalls
}

func (s *reindexStore) chunkIDs(materialID, classID int) []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []int64
	for _, c := range s.chunks {
		if c.MaterialID == materialID && c.ClassID == classID {
			ids = append(ids, c.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (s *reindexStore) statuses(materialID, classID int) []db.IndexStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []db.IndexStatus
	for _, c := range s.chunks {
		if c.MaterialID == materialID && c.ClassID == classID {
			out = append(out, c.IndexStatus)
		}
	}
	return out
}

func (s *reindexStore) strategy(materialID int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.strategies[materialID]
}

func (s *reindexStore) ListChunksByMaterial(_ context.Context, materialID, classID int, status db.IndexStatus) ([]db.Chunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []db.Chunk
	for _, c := range s.chunks {
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

func (s *reindexStore) UpdateChunksStatus(_ context.Context, ids []int64, status db.IndexStatus) error {
	s.mu.Lock()
	isFailure := status == db.IndexFailed
	if isFailure {
		s.markCalls++
		s.recordLocked("status:failed")
	}
	gate, entered := s.markGate, s.markEntered
	s.mu.Unlock()

	if isFailure && gate != nil {
		s.markOnce.Do(func() { close(entered) })
		<-gate
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		for i := range s.chunks {
			if s.chunks[i].ID == id {
				s.chunks[i].IndexStatus = status
			}
		}
	}
	return nil
}

func (s *reindexStore) GetKnowledgeEntryByMaterialID(_ context.Context, materialID int) (*db.KnowledgeEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[materialID]
	if !ok {
		return nil, db.ErrNotFound
	}
	copied := *entry
	return &copied, nil
}

func (s *reindexStore) ReplaceChunksForMaterial(_ context.Context, materialID, classID int, strategy string, _ []byte, chunks []db.Chunk) error {
	s.mu.Lock()
	s.replaceCalls++
	call := s.replaceCalls
	fail := s.replaceFailAt != 0 && call == s.replaceFailAt
	gate, entered := s.replaceGate, s.replaceEntered
	gateApplies := gate != nil && (s.replaceGateMaterial == 0 || s.replaceGateMaterial == materialID)
	s.mu.Unlock()

	if gateApplies {
		s.replaceOnce.Do(func() { close(entered) })
		<-gate
	}

	if fail {
		s.mu.Lock()
		s.recordLocked(fmt.Sprintf("replace:fail:%s", strategy))
		s.mu.Unlock()
		return errors.New("replacement transaction failed")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordLocked(fmt.Sprintf("replace:ok:%s:%d", strategy, len(chunks)))

	kept := s.chunks[:0]
	for _, c := range s.chunks {
		if c.MaterialID == materialID && c.ClassID == classID {
			continue
		}
		kept = append(kept, c)
	}
	s.chunks = kept
	for i := range chunks {
		chunks[i].ID = s.nextID
		s.nextID++
		chunks[i].KnowledgeEntryID = materialID
		chunks[i].MaterialID = materialID
		chunks[i].ClassID = classID
		chunks[i].IndexStatus = db.IndexPending
		s.chunks = append(s.chunks, chunks[i])
	}
	if _, ok := s.entries[materialID]; ok {
		s.strategies[materialID] = strategy
	}
	return nil
}

func (s *reindexStore) GetChunkStats(_ context.Context, materialID, classID int) (db.ChunkStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statsErr != nil {
		return db.ChunkStats{}, s.statsErr
	}
	if _, ok := s.entries[materialID]; !ok {
		return db.ChunkStats{}, db.ErrNotFound
	}
	stats := db.ChunkStats{Strategy: s.strategies[materialID], Status: db.IndexIndexed}
	var failed, pending int
	for _, c := range s.chunks {
		if c.MaterialID != materialID || c.ClassID != classID {
			continue
		}
		stats.Count++
		switch c.IndexStatus {
		case db.IndexFailed:
			failed++
		case db.IndexPending:
			pending++
		}
	}
	switch {
	case failed > 0:
		stats.Status = db.IndexFailed
	case pending > 0 || stats.Count == 0:
		stats.Status = db.IndexPending
	}
	if stats.Strategy == "" {
		stats.Strategy = string(knowledge.StrategyAuto)
	}
	return stats, nil
}

// recordingVectors implements both index.VectorStore (Upsert) and
// index.VectorDeleter (DeleteByMaterial), and keeps one point set per
// material so tests can assert exact generation replacement.
type recordingVectors struct {
	mu sync.Mutex

	points       map[int]map[int64]vector.Point
	deleteCalls  int
	upsertCalls  int
	deleteFailAt int
	upsertFailAt int

	deleteGate    <-chan struct{}
	deleteEntered chan struct{}
	deleteOnce    sync.Once

	upsertGate    <-chan struct{}
	upsertEntered chan struct{}
	upsertOnce    sync.Once

	opLog []string
}

func newRecordingVectors() *recordingVectors {
	return &recordingVectors{
		points: map[int]map[int64]vector.Point{},
	}
}

func (v *recordingVectors) recordLocked(op string) {
	v.opLog = append(v.opLog, op)
}

func (v *recordingVectors) opCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.opLog)
}

func (v *recordingVectors) opIndex(prefix string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	for i, op := range v.opLog {
		if strings.HasPrefix(op, prefix) {
			return i
		}
	}
	return -1
}

func (v *recordingVectors) deleteCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.deleteCalls
}

func (v *recordingVectors) upsertCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.upsertCalls
}

func (v *recordingVectors) pointIDs(materialID int) []int64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	ids := make([]int64, 0, len(v.points[materialID]))
	for id := range v.points[materialID] {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (v *recordingVectors) pointsOf(materialID int) []vector.Point {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]vector.Point, 0, len(v.points[materialID]))
	for _, p := range v.points[materialID] {
		out = append(out, p)
	}
	return out
}

func (v *recordingVectors) DeleteByMaterial(_ context.Context, materialID, classID int) error {
	v.mu.Lock()
	v.deleteCalls++
	call := v.deleteCalls
	fail := v.deleteFailAt != 0 && call == v.deleteFailAt
	gate, entered := v.deleteGate, v.deleteEntered
	v.mu.Unlock()

	if gate != nil {
		v.deleteOnce.Do(func() { close(entered) })
		<-gate
	}

	if fail {
		v.mu.Lock()
		v.recordLocked(fmt.Sprintf("delete:fail:%d", materialID))
		v.mu.Unlock()
		return errors.New("vector store is down")
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	v.recordLocked(fmt.Sprintf("delete:%d", materialID))
	delete(v.points, materialID)
	return nil
}

func (v *recordingVectors) Upsert(_ context.Context, points []vector.Point) error {
	v.mu.Lock()
	v.upsertCalls++
	call := v.upsertCalls
	fail := v.upsertFailAt != 0 && call == v.upsertFailAt
	gate, entered := v.upsertGate, v.upsertEntered
	v.mu.Unlock()

	if gate != nil {
		v.upsertOnce.Do(func() { close(entered) })
		<-gate
	}
	if fail {
		v.mu.Lock()
		v.recordLocked("upsert:fail")
		v.mu.Unlock()
		return errors.New("vector write failed")
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if len(points) > 0 {
		materialID := points[0].Payload.MaterialID
		v.recordLocked(fmt.Sprintf("upsert:%d", materialID))
		if v.points[materialID] == nil {
			v.points[materialID] = map[int64]vector.Point{}
		}
		for _, p := range points {
			v.points[materialID][p.ID] = p
		}
	}
	return nil
}

// gatedEmbedder blocks one chosen call after recording its input.
type gatedEmbedder struct {
	mu sync.Mutex

	callNo    int
	failAt    int
	blockAt   int
	gate      <-chan struct{}
	entered   chan struct{}
	enterOnce sync.Once
}

func (e *gatedEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	e.mu.Lock()
	e.callNo++
	call := e.callNo
	fail := e.failAt != 0 && call == e.failAt
	e.mu.Unlock()

	if e.blockAt != 0 && call == e.blockAt && e.gate != nil {
		e.enterOnce.Do(func() { close(e.entered) })
		select {
		case <-e.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if fail {
		return nil, errors.New("embedding gateway is down")
	}

	out := make([][]float64, len(texts))
	for i := range out {
		out[i] = []float64{1, 0}
	}
	return out, nil
}

func (e *gatedEmbedder) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.callNo
}

type reindexFixture struct {
	store    *reindexStore
	vectors  *recordingVectors
	embedder *gatedEmbedder
	indexer  *Indexer
	material int
	class    int
	body     string
}

func newReindexFixture(t *testing.T, body string, pendingChunks int) *reindexFixture {
	t.Helper()
	const (
		materialID = 1
		classID    = 7
	)
	f := &reindexFixture{
		store:    newReindexStore(materialID, classID, body),
		vectors:  newRecordingVectors(),
		embedder: &gatedEmbedder{},
		material: materialID,
		class:    classID,
		body:     body,
	}
	f.store.seedPending(materialID, classID, pendingChunks)
	f.indexer = New(f.store, f.embedder, f.vectors, 32, 5*time.Second)
	return f
}

func customParams(size int) knowledge.ChunkParams {
	return knowledge.ChunkParams{Strategy: knowledge.StrategyCustom, ChunkSize: size, OverlapPercent: 10}
}

func (f *reindexFixture) reindex(ctx context.Context, params knowledge.ChunkParams) (db.ChunkStats, error) {
	return f.indexer.Reindex(ctx, ReindexRequest{
		MaterialID: f.material,
		ClassID:    f.class,
		Params:     params,
		Store:      f.store,
		Vectors:    f.vectors,
	})
}

// waitClosed waits for a barrier signal, bounded so a broken implementation
// fails instead of hanging the suite.
func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// assertNoNewOps fails if the observed operation count changes during a short
// window. The interleaving this window observes is fixed by the caller's
// barriers; the window itself is only a failure detector.
func assertNoNewOps(t *testing.T, count func() int, what string) {
	t.Helper()
	baseline := count()
	select {
	case <-time.After(100 * time.Millisecond):
		if got := count(); got != baseline {
			t.Fatalf("%s performed %d new operations while it should have been waiting for the material lock", what, got-baseline)
		}
	}
}

func waitErr(t *testing.T, errCh <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-errCh:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return nil
	}
}

func assertSameIDs(t *testing.T, got, want []int64, what string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s has %d ids %v, want %d ids %v", what, len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

// TestIndexMaterialAndReindexShareTheMaterialLock is the "普通索引与重建共用
// 互斥" scenario: an ordinary index (upload or compensation) holds the lock
// while it embeds, and the rebuild must not delete the vectors it is about to
// write.
func TestIndexMaterialAndReindexShareTheMaterialLock(t *testing.T) {
	f := newReindexFixture(t, strings.Repeat("甲", 900), 3)

	gate := make(chan struct{})
	f.embedder.blockAt = 1
	f.embedder.gate = gate
	f.embedder.entered = make(chan struct{})

	indexErr := make(chan error, 1)
	go func() { indexErr <- f.indexer.IndexMaterial(context.Background(), f.material, f.class) }()
	waitClosed(t, f.embedder.entered, "the ordinary index to reach embedding")

	started := make(chan struct{})
	errReindex := make(chan error, 1)
	var reindexStats db.ChunkStats
	go func() {
		close(started)
		var err error
		reindexStats, err = f.reindex(context.Background(), customParams(200))
		errReindex <- err
	}()
	waitClosed(t, started, "the rebuild goroutine to start")

	// The rebuild is queued behind the ordinary index: no deletion, no
	// replacement, no embedding of the new chunks yet.
	ops := func() int { return f.store.opCount() + f.vectors.opCount() }
	assertNoNewOps(t, ops, "the queued rebuild")
	if got := f.store.replaceCount(); got != 0 {
		t.Fatalf("the queued rebuild ran %d replacements before the ordinary index finished", got)
	}

	close(gate)
	if err := waitErr(t, indexErr, "the ordinary index"); err != nil {
		t.Fatalf("IndexMaterial failed: %v", err)
	}
	if err := waitErr(t, errReindex, "the rebuild"); err != nil {
		t.Fatalf("Reindex failed: %v", err)
	}

	// Only the rebuild's generation survives, in both stores.
	current := f.store.chunkIDs(f.material, f.class)
	assertSameIDs(t, f.vectors.pointIDs(f.material), current, "vector point ids")
	if reindexStats.Strategy != string(knowledge.StrategyCustom) {
		t.Fatalf("reindex stats strategy = %q, want custom", reindexStats.Strategy)
	}
	for _, status := range f.store.statuses(f.material, f.class) {
		if status != db.IndexIndexed {
			t.Fatalf("chunk status = %q, want indexed", status)
		}
	}
}

// TestReindexWaitsBetweenDeleteAndReplace pins the second interleaving: A has
// already deleted the vectors but has not replaced the chunks, so B must not
// start a second delete/replace/index sequence.
func TestReindexWaitsBetweenDeleteAndReplace(t *testing.T) {
	f := newReindexFixture(t, strings.Repeat("乙", 900), 3)

	gate := make(chan struct{})
	f.store.replaceGate = gate
	f.store.replaceEntered = make(chan struct{})

	var statsA db.ChunkStats
	errA := make(chan error, 1)
	go func() {
		var err error
		statsA, err = f.reindex(context.Background(), customParams(200))
		errA <- err
	}()
	waitClosed(t, f.store.replaceEntered, "A to reach the replacement step")
	if got := f.vectors.deleteCount(); got != 1 {
		t.Fatalf("delete count = %d, want A's single delete", got)
	}

	started := make(chan struct{})
	var statsB db.ChunkStats
	errB := make(chan error, 1)
	go func() {
		close(started)
		var err error
		statsB, err = f.reindex(context.Background(), customParams(400))
		errB <- err
	}()
	waitClosed(t, started, "B to start")
	ops := func() int { return f.store.opCount() + f.vectors.opCount() }
	assertNoNewOps(t, ops, "B while A sits between delete and replace")
	if got := f.vectors.deleteCount(); got != 1 {
		t.Fatalf("B deleted vectors while A was between delete and replace: %d deletes", got)
	}
	if got := f.store.replaceCount(); got != 1 {
		t.Fatalf("B replaced chunks while A was paused: %d replacements", got)
	}

	close(gate)
	if err := waitErr(t, errA, "A"); err != nil {
		t.Fatalf("A failed: %v", err)
	}
	if err := waitErr(t, errB, "B"); err != nil {
		t.Fatalf("B failed: %v", err)
	}

	// Each response describes its own version: A ran first, then B.
	if statsA.Strategy != string(knowledge.StrategyCustom) || statsA.Count != len(knowledge.ChunkBody(f.body, customParams(200))) {
		t.Fatalf("A stats = %+v, want its own custom 200 version", statsA)
	}
	if statsB.Strategy != string(knowledge.StrategyCustom) || statsB.Count != len(knowledge.ChunkBody(f.body, customParams(400))) {
		t.Fatalf("B stats = %+v, want its own custom 400 version", statsB)
	}
	if statsA.Count == statsB.Count {
		t.Fatalf("A and B report the same chunk count %d; the body should produce different windows", statsA.Count)
	}
	assertSameIDs(t, f.vectors.pointIDs(f.material), f.store.chunkIDs(f.material, f.class),
		"vector point ids after the serialized rebuilds")
	if got := f.store.strategy(f.material); got != string(knowledge.StrategyCustom) {
		t.Fatalf("final strategy = %q", got)
	}
	if got := f.store.replaceCount(); got != 2 {
		t.Fatalf("replace count = %d, want exactly 2 (one per rebuild)", got)
	}
}

// TestConcurrentReindexesLeaveOnlyTheLastGeneration pauses A after it has
// replaced the chunks and while it embeds the new ones, then starts B. B may
// only run after A's whole pipeline, and the final point set must be exactly
// B's current chunk set.
func TestConcurrentReindexesLeaveOnlyTheLastGeneration(t *testing.T) {
	f := newReindexFixture(t, strings.Repeat("丙", 900), 3)
	paramsA := customParams(200)
	paramsB := customParams(400)

	gate := make(chan struct{})
	f.embedder.blockAt = 1 // A's embedding of its new chunks
	f.embedder.gate = gate
	f.embedder.entered = make(chan struct{})

	var statsA db.ChunkStats
	errA := make(chan error, 1)
	go func() {
		var err error
		statsA, err = f.reindex(context.Background(), paramsA)
		errA <- err
	}()
	waitClosed(t, f.embedder.entered, "A to reach the embedding step")
	idsA := f.store.chunkIDs(f.material, f.class)
	if len(idsA) == 0 {
		t.Fatal("A replaced the chunks with nothing")
	}
	if f.vectors.upsertCount() != 0 {
		t.Fatal("A wrote vectors before the embedding barrier was released")
	}

	started := make(chan struct{})
	var statsB db.ChunkStats
	errB := make(chan error, 1)
	go func() {
		close(started)
		var err error
		statsB, err = f.reindex(context.Background(), paramsB)
		errB <- err
	}()
	waitClosed(t, started, "B to start")
	ops := func() int { return f.store.opCount() + f.vectors.opCount() }
	assertNoNewOps(t, ops, "B while A is still indexing its generation")
	if got := f.vectors.deleteCount(); got != 1 {
		t.Fatalf("B deleted vectors while A was still indexing: %d deletes", got)
	}

	close(gate)
	if err := waitErr(t, errA, "A"); err != nil {
		t.Fatalf("A failed: %v", err)
	}
	if err := waitErr(t, errB, "B"); err != nil {
		t.Fatalf("B failed: %v", err)
	}

	idsB := f.store.chunkIDs(f.material, f.class)
	assertSameIDs(t, f.vectors.pointIDs(f.material), idsB, "final vector point ids")
	for _, id := range idsA {
		for _, pointID := range f.vectors.pointIDs(f.material) {
			if pointID == id {
				t.Fatalf("old-generation point %d survived", id)
			}
		}
	}
	for _, p := range f.vectors.pointsOf(f.material) {
		if p.Payload.ClassID != f.class || p.Payload.MaterialID != f.material {
			t.Fatalf("point payload = %+v, want material %d class %d", p.Payload, f.material, f.class)
		}
	}
	if statsA.Count != len(knowledge.ChunkBody(f.body, paramsA)) || statsA.Strategy != string(knowledge.StrategyCustom) {
		t.Fatalf("A stats = %+v, want A's own version", statsA)
	}
	if statsB.Count != len(idsB) || statsB.Strategy != string(knowledge.StrategyCustom) {
		t.Fatalf("B stats = %+v, want B's own version with %d chunks", statsB, len(idsB))
	}
	for _, status := range f.store.statuses(f.material, f.class) {
		if status != db.IndexIndexed {
			t.Fatalf("final chunk status = %q, want indexed", status)
		}
	}
}

// TestReindexesOfDifferentMaterialsProceedIndependently keeps one material
// paused and checks that rebuilds of other materials (in this and another
// class) complete without waiting.
func TestReindexesOfDifferentMaterialsProceedIndependently(t *testing.T) {
	f := newReindexFixture(t, strings.Repeat("丁", 900), 2)
	const (
		otherSameClass = 2
		otherClass     = 8
	)
	f.store.addEntry(otherSameClass, f.class, strings.Repeat("戊", 700))
	f.store.addEntry(otherClass, otherClass, strings.Repeat("己", 700))
	f.store.seedPending(otherSameClass, f.class, 1)
	f.store.seedPending(otherClass, otherClass, 1)

	gate := make(chan struct{})
	f.store.replaceGate = gate
	f.store.replaceEntered = make(chan struct{})
	f.store.replaceGateMaterial = f.material

	errM := make(chan error, 1)
	go func() {
		_, err := f.reindex(context.Background(), customParams(200))
		errM <- err
	}()
	waitClosed(t, f.store.replaceEntered, "material M to reach the replacement step")

	type result struct {
		stats db.ChunkStats
		err   error
	}
	done := make(chan result, 2)
	go func() {
		stats, err := f.indexer.Reindex(context.Background(), ReindexRequest{
			MaterialID: otherSameClass, ClassID: f.class,
			Params: customParams(200), Store: f.store, Vectors: f.vectors,
		})
		done <- result{stats, err}
	}()
	go func() {
		stats, err := f.indexer.Reindex(context.Background(), ReindexRequest{
			MaterialID: otherClass, ClassID: otherClass,
			Params: customParams(200), Store: f.store, Vectors: f.vectors,
		})
		done <- result{stats, err}
	}()

	for i := 0; i < 2; i++ {
		select {
		case res := <-done:
			if res.err != nil {
				t.Fatalf("an unrelated material's rebuild failed while M was paused: %v", res.err)
			}
			if res.stats.Status != db.IndexIndexed {
				t.Fatalf("unrelated rebuild stats = %+v, want indexed", res.stats)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("an unrelated material's rebuild blocked behind M")
		}
	}

	// Each material's point set only contains its own current chunks, and M is
	// still between delete and replace.
	assertSameIDs(t, f.vectors.pointIDs(otherSameClass), f.store.chunkIDs(otherSameClass, f.class), "material N point ids")
	assertSameIDs(t, f.vectors.pointIDs(otherClass), f.store.chunkIDs(otherClass, otherClass), "material P point ids")

	close(gate)
	if err := <-errM; err != nil {
		t.Fatalf("M failed: %v", err)
	}
	assertSameIDs(t, f.vectors.pointIDs(f.material), f.store.chunkIDs(f.material, f.class), "material M point ids")
}

// TestCanceledReindexWaiterDoesNoDestructiveWork queues B behind A, cancels B
// while it waits, and checks that B does nothing when it finally gets the lock
// while C completes normally.
func TestCanceledReindexWaiterDoesNoDestructiveWork(t *testing.T) {
	f := newReindexFixture(t, strings.Repeat("庚", 900), 2)

	gate := make(chan struct{})
	f.store.replaceGate = gate
	f.store.replaceEntered = make(chan struct{})

	errA := make(chan error, 1)
	go func() {
		_, err := f.reindex(context.Background(), customParams(200))
		errA <- err
	}()
	waitClosed(t, f.store.replaceEntered, "A to hold the material lock")

	ctxB, cancelB := context.WithCancel(context.Background())
	startedB := make(chan struct{})
	errB := make(chan error, 1)
	go func() {
		close(startedB)
		_, err := f.reindex(ctxB, customParams(300))
		errB <- err
	}()
	waitClosed(t, startedB, "B to start waiting")
	cancelB()

	startedC := make(chan struct{})
	errC := make(chan error, 1)
	go func() {
		close(startedC)
		_, err := f.reindex(context.Background(), customParams(400))
		errC <- err
	}()
	waitClosed(t, startedC, "C to start waiting")

	close(gate)
	if err := waitErr(t, errA, "A"); err != nil {
		t.Fatalf("A failed: %v", err)
	}
	if err := waitErr(t, errB, "B"); !errors.Is(err, context.Canceled) {
		t.Fatalf("B returned %v, want context.Canceled", err)
	}
	if err := waitErr(t, errC, "C"); err != nil {
		t.Fatalf("C failed: %v", err)
	}

	if idx := f.store.opIndex(fmt.Sprintf("replace:ok:custom:%d", len(knowledge.ChunkBody(f.body, customParams(300))))); idx != -1 {
		t.Fatalf("the canceled waiter performed a replacement at op %d", idx)
	}
	if got := f.store.strategy(f.material); got != string(knowledge.StrategyCustom) {
		t.Fatalf("final strategy = %q", got)
	}
	assertSameIDs(t, f.vectors.pointIDs(f.material), f.store.chunkIDs(f.material, f.class),
		"final point ids after B was canceled")
	if got := len(f.store.chunkIDs(f.material, f.class)); got != len(knowledge.ChunkBody(f.body, customParams(400))) {
		t.Fatalf("final chunks came from %d windows, want C's 400-character windows (%d)",
			got, len(knowledge.ChunkBody(f.body, customParams(400))))
	}
}

// TestReindexWithAnAlreadyCanceledContextDoesNothing covers the case where the
// request is canceled before it even reaches the lock.
func TestReindexWithAnAlreadyCanceledContextDoesNothing(t *testing.T) {
	f := newReindexFixture(t, strings.Repeat("辛", 900), 2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := f.reindex(ctx, customParams(200)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reindex returned %v, want context.Canceled", err)
	}
	if f.vectors.deleteCount() != 0 || f.store.replaceCount() != 0 || f.embedder.callCount() != 0 {
		t.Fatal("a canceled request touched a dependency")
	}
}

// TestReindexAfterVectorDeleteFailureCanProceed: A fails before touching MySQL;
// the queued B then rebuilds normally.
func TestReindexAfterVectorDeleteFailureCanProceed(t *testing.T) {
	f := newReindexFixture(t, strings.Repeat("壬", 900), 2)
	f.vectors.deleteFailAt = 1
	deleteGate := make(chan struct{})
	f.vectors.deleteGate = deleteGate
	f.vectors.deleteEntered = make(chan struct{})

	errA := make(chan error, 1)
	go func() {
		_, err := f.reindex(context.Background(), customParams(200))
		errA <- err
	}()
	waitClosed(t, f.vectors.deleteEntered, "A to reach the vector deletion")
	startedB := make(chan struct{})
	var statsB db.ChunkStats
	errB := make(chan error, 1)
	go func() {
		close(startedB)
		var err error
		statsB, err = f.reindex(context.Background(), customParams(400))
		errB <- err
	}()
	waitClosed(t, startedB, "B to start")
	close(deleteGate)

	if err := waitErr(t, errA, "A"); err == nil {
		t.Fatal("A succeeded although deleting the vectors failed")
	}
	if err := waitErr(t, errB, "B"); err != nil {
		t.Fatalf("B failed after A's delete failure: %v", err)
	}

	assertSameIDs(t, f.vectors.pointIDs(f.material), f.store.chunkIDs(f.material, f.class), "final point ids")
	if statsB.Status != db.IndexIndexed {
		t.Fatalf("B stats = %+v, want indexed", statsB)
	}
	if idx := f.store.opIndex("replace:fail"); idx != -1 {
		t.Fatalf("A replaced chunks although the vector deletion failed (op %d)", idx)
	}
}

// TestReindexAfterReplaceFailureCanProceed: A deletes the vectors, then the
// replacement transaction fails. The old chunks are marked failed inside the
// critical section; B then rebuilds normally and its indexed status must not
// be overwritten by A's failure marking.
func TestReindexAfterReplaceFailureCanProceed(t *testing.T) {
	f := newReindexFixture(t, strings.Repeat("癸", 900), 2)
	f.store.replaceFailAt = 1
	replaceGate := make(chan struct{})
	f.store.replaceGate = replaceGate
	f.store.replaceEntered = make(chan struct{})

	errA := make(chan error, 1)
	go func() {
		_, err := f.reindex(context.Background(), customParams(200))
		errA <- err
	}()
	waitClosed(t, f.store.replaceEntered, "A to reach the replacement step")
	startedB := make(chan struct{})
	var statsB db.ChunkStats
	errB := make(chan error, 1)
	go func() {
		close(startedB)
		var err error
		statsB, err = f.reindex(context.Background(), customParams(400))
		errB <- err
	}()
	waitClosed(t, startedB, "B to start")
	close(replaceGate)

	if err := waitErr(t, errA, "A"); err == nil {
		t.Fatal("A succeeded although the replacement transaction failed")
	}
	if err := waitErr(t, errB, "B"); err != nil {
		t.Fatalf("B failed after A's replacement failure: %v", err)
	}

	replaceFail := f.store.opIndex("replace:fail")
	markOp := f.store.opIndex("status:failed")
	bReplace := f.store.opIndex("replace:ok:")
	if replaceFail == -1 || markOp == -1 || bReplace == -1 {
		t.Fatalf("missing operations: fail=%d mark=%d bReplace=%d (%v)", replaceFail, markOp, bReplace, f.store.opLog)
	}
	if !(replaceFail < markOp && markOp < bReplace) {
		t.Fatalf("expected failure < failure marking < B's replacement, got %d/%d/%d", replaceFail, markOp, bReplace)
	}
	if statsB.Status != db.IndexIndexed {
		t.Fatalf("B stats = %+v, want indexed (A's late marking overwrote B)", statsB)
	}
	for _, status := range f.store.statuses(f.material, f.class) {
		if status != db.IndexIndexed {
			t.Fatalf("final chunk status = %q, want indexed", status)
		}
	}
	assertSameIDs(t, f.vectors.pointIDs(f.material), f.store.chunkIDs(f.material, f.class), "final point ids")
}

// TestIndexFailureMarkingCompletesBeforeLockRelease pauses A inside its failure
// marking, starts B, and checks that B cannot start until A's marking is done
// and that B's final state is not overwritten.
func TestIndexFailureMarkingCompletesBeforeLockRelease(t *testing.T) {
	f := newReindexFixture(t, strings.Repeat("子", 900), 2)
	f.embedder.failAt = 1 // A's indexing of the new chunks fails

	gate := make(chan struct{})
	f.store.markGate = gate
	f.store.markEntered = make(chan struct{})

	var statsA db.ChunkStats
	errA := make(chan error, 1)
	go func() {
		var err error
		statsA, err = f.reindex(context.Background(), customParams(200))
		errA <- err
	}()
	waitClosed(t, f.store.markEntered, "A to enter failure marking")

	startedB := make(chan struct{})
	statsB := make(chan db.ChunkStats, 1)
	errB := make(chan error, 1)
	go func() {
		close(startedB)
		stats, err := f.reindex(context.Background(), customParams(400))
		statsB <- stats
		errB <- err
	}()
	waitClosed(t, startedB, "B to start")
	ops := func() int { return f.store.opCount() + f.vectors.opCount() }
	assertNoNewOps(t, ops, "B while A finalizes its failure marking")
	if got := f.store.replaceCount(); got != 1 {
		t.Fatalf("B replaced chunks before A's failure marking finished: %d replacements", got)
	}

	close(gate)
	if err := waitErr(t, errA, "A"); err != nil {
		t.Fatalf("A returned %v; an indexing dependency failure must not fail the rebuild", err)
	}
	if statsA.Status != db.IndexFailed {
		t.Fatalf("A stats = %+v, want failed", statsA)
	}
	if err := waitErr(t, errB, "B"); err != nil {
		t.Fatalf("B failed: %v", err)
	}
	if got := <-statsB; got.Status != db.IndexIndexed {
		t.Fatalf("B stats = %+v, want indexed", got)
	}
	if got := f.store.markCount(); got != 1 {
		t.Fatalf("failure marking ran %d times, want once for A", got)
	}
	for _, status := range f.store.statuses(f.material, f.class) {
		if status != db.IndexIndexed {
			t.Fatalf("final chunk status = %q, want indexed (A's failure marking leaked past B)", status)
		}
	}
	assertSameIDs(t, f.vectors.pointIDs(f.material), f.store.chunkIDs(f.material, f.class), "final point ids")
}

// TestReindexRechecksClassUnderTheLock covers the identity re-check: when the
// knowledge entry belongs to another class by the time the lock is acquired,
// nothing may be deleted or replaced.
func TestReindexRechecksClassUnderTheLock(t *testing.T) {
	f := newReindexFixture(t, strings.Repeat("丑", 900), 2)
	f.store.entries[f.material].ClassID = 99

	_, err := f.indexer.Reindex(context.Background(), ReindexRequest{
		MaterialID: f.material,
		ClassID:    f.class,
		Params:     customParams(200),
		Store:      f.store,
		Vectors:    f.vectors,
	})
	if !errors.Is(err, ErrReindexNotFound) {
		t.Fatalf("Reindex returned %v, want ErrReindexNotFound", err)
	}
	if f.vectors.deleteCount() != 0 || f.store.replaceCount() != 0 {
		t.Fatal("a class-mismatched rebuild touched a store")
	}
}
