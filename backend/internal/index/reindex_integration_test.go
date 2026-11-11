package index

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/knowledge"
	"campusclaw/backend/internal/search"
	"campusclaw/backend/internal/vector"
)

// This is the end-to-end acceptance test for the concurrent-rebuild scenarios
// (tasks.md 3.4). It needs a real MySQL and Qdrant, but not the embedding
// stub: the embedder is deterministic and the barrier pauses the first rebuild
// inside its indexing phase.
//
//	CAMPUSCLAW_TEST_DSN=app:pass@tcp(db:3306)/campusclaw?parseTime=true&charset=utf8mb4
//	CAMPUSCLAW_TEST_QDRANT_URL=http://qdrant:6333
//	CAMPUSCLAW_TEST_EMBEDDING_DIM=256

// detEmbedder returns a one-hot vector: chunks containing the marker "目标片段"
// point at dimension 0, everything else at dimension 1. A query embedding for
// the marker therefore matches only the current chunk that contains it, with
// cosine score 1 (everything else scores 0 and is below the search threshold).
type detEmbedder struct {
	mu sync.Mutex

	dim     int
	callNo  int
	blockAt int
	gate    <-chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (e *detEmbedder) vectorFor(text string) []float64 {
	v := make([]float64, e.dim)
	if strings.Contains(text, "目标片段") {
		v[0] = 1
	} else if e.dim > 1 {
		v[1] = 1
	} else {
		v[0] = 1
	}
	return v
}

func (e *detEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	e.mu.Lock()
	e.callNo++
	call := e.callNo
	block := e.blockAt != 0 && call == e.blockAt && e.gate != nil
	e.mu.Unlock()

	if block {
		e.once.Do(func() { close(e.entered) })
		select {
		case <-e.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	out := make([][]float64, len(texts))
	for i, text := range texts {
		out[i] = e.vectorFor(text)
	}
	return out, nil
}

func (e *detEmbedder) EmbedOne(_ context.Context, text string) ([]float64, error) {
	return e.vectorFor(text), nil
}

// arm blocks the next Embed call after the call that has already run.
func (e *detEmbedder) arm(gate <-chan struct{}, entered chan struct{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.blockAt = e.callNo + 1
	e.gate = gate
	e.entered = entered
}

type reindexIntegrationEnv struct {
	conn      *sql.DB
	indexer   *Indexer
	vectors   *vector.Client
	embedder  *detEmbedder
	engine    *search.Engine
	qdrantURL string
	classA    int
	teacherA  int
}

func openReindexIntegrationEnv(t *testing.T) *reindexIntegrationEnv {
	t.Helper()

	dsn := os.Getenv("CAMPUSCLAW_TEST_DSN")
	qdrantURL := os.Getenv("CAMPUSCLAW_TEST_QDRANT_URL")
	if dsn == "" || qdrantURL == "" {
		t.Skip("CAMPUSCLAW_TEST_DSN and CAMPUSCLAW_TEST_QDRANT_URL must both be set")
	}
	dim := 256
	if v := os.Getenv("CAMPUSCLAW_TEST_EMBEDDING_DIM"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed <= 0 {
			t.Fatalf("CAMPUSCLAW_TEST_EMBEDDING_DIM=%q is not a positive integer", v)
		}
		dim = parsed
	}

	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping test db: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	env := &reindexIntegrationEnv{conn: conn, qdrantURL: qdrantURL}
	ctx := context.Background()
	if err := conn.QueryRowContext(ctx, `SELECT id FROM classes WHERE name = ?`, "A班").Scan(&env.classA); err != nil {
		t.Skipf("seeded class A missing (%v); is this a seeded database?", err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, "teacher_a").Scan(&env.teacherA); err != nil {
		t.Skipf("seeded teacher_a missing (%v); is this a seeded database?", err)
	}

	env.embedder = &detEmbedder{dim: dim}
	env.vectors = vector.NewClient(qdrantURL, "", dim, 10*time.Second)
	env.indexer = New(SQLChunkStore{Conn: conn}, env.embedder, env.vectors, 32, 30*time.Second)
	env.engine = search.New(conn, env.embedder, env.vectors)
	return env
}

func (e *reindexIntegrationEnv) createMaterial(t *testing.T, title, body string) (int, []db.Chunk) {
	t.Helper()
	ctx := context.Background()

	params := knowledge.DefaultParams()
	produced := knowledge.ChunkBody(body, params)
	chunks := make([]db.Chunk, 0, len(produced))
	for _, c := range produced {
		chunks = append(chunks, db.Chunk{
			ChunkIndex: c.Index,
			CharStart:  c.Span.Start,
			CharEnd:    c.Span.End,
			ChunkText:  c.Text,
		})
	}

	materialID, err := db.InsertMaterialWithChunks(ctx, e.conn, db.NewMaterial{
		ClassID:       e.classA,
		Title:         title,
		StoredName:    title + ".md",
		OriginalName:  title + ".md",
		SizeBytes:     int64(len(body)),
		UploadedBy:    e.teacherA,
		BodyText:      body,
		ChunkStrategy: string(params.Strategy),
		ChunkParams:   params.ParamsJSON(),
		Chunks:        chunks,
	})
	if err != nil {
		t.Fatalf("insert material: %v", err)
	}

	stored, err := db.ListChunksByMaterial(ctx, e.conn, materialID, e.classA, "")
	if err != nil {
		t.Fatalf("list inserted chunks: %v", err)
	}

	t.Cleanup(func() {
		if _, err := e.conn.ExecContext(context.Background(), `DELETE FROM materials WHERE id = ?`, materialID); err != nil {
			t.Errorf("cleanup material %d: %v", materialID, err)
		}
		if err := vector.NewClient(e.qdrantURL, "", e.embedder.dim, 10*time.Second).
			DeleteByMaterial(context.Background(), materialID, e.classA); err != nil {
			t.Errorf("cleanup vectors of material %d: %v", materialID, err)
		}
	})
	return materialID, stored
}

func (e *reindexIntegrationEnv) currentChunkIDs(t *testing.T, materialID int) []int64 {
	t.Helper()
	chunks, err := db.ListChunksByMaterial(context.Background(), e.conn, materialID, e.classA, "")
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	ids := make([]int64, 0, len(chunks))
	for _, c := range chunks {
		ids = append(ids, c.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

type storedPoint struct {
	ID      int64               `json:"id"`
	Payload vector.PointPayload `json:"payload"`
}

// scrollPoints enumerates every point of one material in the class through the
// raw Qdrant API, independent of the client under test.
func (e *reindexIntegrationEnv) scrollPoints(t *testing.T, materialID int) []storedPoint {
	t.Helper()
	var out []storedPoint
	var offset any
	for {
		body := map[string]any{
			"limit":        100,
			"with_payload": true,
			"filter": map[string]any{"must": []any{
				map[string]any{"key": "material_id", "match": map[string]any{"value": materialID}},
				map[string]any{"key": "class_id", "match": map[string]any{"value": e.classA}},
			}},
		}
		if offset != nil {
			body["offset"] = offset
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode scroll body: %v", err)
		}
		resp, err := http.Post(e.qdrantURL+"/collections/"+vector.CollectionName+"/points/scroll",
			"application/json", bytes.NewReader(encoded))
		if err != nil {
			t.Fatalf("scroll points: %v", err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("scroll status = %d: %s", resp.StatusCode, raw)
		}
		var decoded struct {
			Result struct {
				Points []storedPoint `json:"points"`
				Next   any           `json:"next_page_offset"`
			} `json:"result"`
		}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("decode scroll response %s: %v", raw, err)
		}
		out = append(out, decoded.Result.Points...)
		if decoded.Result.Next == nil {
			return out
		}
		offset = decoded.Result.Next
	}
}

func pointIDs(points []storedPoint) []int64 {
	ids := make([]int64, 0, len(points))
	for _, p := range points {
		ids = append(ids, p.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func containsID(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func (e *reindexIntegrationEnv) chunkParams(t *testing.T, materialID int) map[string]any {
	t.Helper()
	var raw []byte
	if err := e.conn.QueryRowContext(context.Background(),
		`SELECT chunk_params FROM knowledge_entries WHERE material_id = ?`, materialID).Scan(&raw); err != nil {
		t.Fatalf("read chunk params: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode chunk params %s: %v", raw, err)
	}
	return decoded
}

// TestReindexIntegrationConcurrentRebuildsLeaveOnlyTheLastGeneration fixes the
// race where A has replaced its chunks and is still writing vectors: B must
// wait, then its generation must be the only one left in Qdrant.
func TestReindexIntegrationConcurrentRebuildsLeaveOnlyTheLastGeneration(t *testing.T) {
	env := openReindexIntegrationEnv(t)
	body := strings.Repeat("甲", 500) + "目标片段" + strings.Repeat("乙", 500)
	materialID, initialChunks := env.createMaterial(t, "回归-并发重建-集成", body)

	// Generation 0: the material is uploaded and indexed.
	if err := env.indexer.IndexMaterial(context.Background(), materialID, env.classA); err != nil {
		t.Fatalf("initial index: %v", err)
	}
	initialIDs := make([]int64, 0, len(initialChunks))
	for _, c := range initialChunks {
		initialIDs = append(initialIDs, c.ID)
	}
	sort.Slice(initialIDs, func(i, j int) bool { return initialIDs[i] < initialIDs[j] })
	if got := pointIDs(env.scrollPoints(t, materialID)); len(got) != len(initialIDs) {
		t.Fatalf("initial vector count = %d, want %d", len(got), len(initialIDs))
	}

	paramsA := knowledge.ChunkParams{Strategy: knowledge.StrategyCustom, ChunkSize: 200, OverlapPercent: 10}
	paramsB := knowledge.ChunkParams{Strategy: knowledge.StrategyCustom, ChunkSize: 400, OverlapPercent: 10}

	gate := make(chan struct{})
	entered := make(chan struct{})
	env.embedder.arm(gate, entered)

	var statsA, statsB db.ChunkStats
	errA := make(chan error, 1)
	errB := make(chan error, 1)
	go func() {
		var err error
		statsA, err = env.indexer.Reindex(context.Background(), ReindexRequest{
			MaterialID: materialID, ClassID: env.classA, Params: paramsA,
			Store: SQLReindexStore{Conn: env.conn}, Vectors: env.vectors,
		})
		errA <- err
	}()
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("A did not reach its vector-write barrier")
	}

	idsA := env.currentChunkIDs(t, materialID)
	if len(idsA) == 0 {
		t.Fatal("A replaced the chunks with nothing")
	}

	go func() {
		var err error
		statsB, err = env.indexer.Reindex(context.Background(), ReindexRequest{
			MaterialID: materialID, ClassID: env.classA, Params: paramsB,
			Store: SQLReindexStore{Conn: env.conn}, Vectors: env.vectors,
		})
		errB <- err
	}()

	// While A is paused, B must not have deleted or replaced anything: the
	// point set is still exactly A's current chunks.
	select {
	case err := <-errB:
		t.Fatalf("B finished while A was paused: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if got := pointIDs(env.scrollPoints(t, materialID)); !sameIDs(got, idsA) {
		t.Fatalf("B changed the point set while A was paused: got %v, want A's %v", got, idsA)
	}

	close(gate)
	select {
	case err := <-errA:
		if err != nil {
			t.Fatalf("A failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("A did not finish")
	}
	select {
	case err := <-errB:
		if err != nil {
			t.Fatalf("B failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("B did not finish")
	}

	// Both responses describe their own generation.
	if statsA.Count != len(knowledge.ChunkBody(body, paramsA)) || statsA.Strategy != "custom" {
		t.Fatalf("A stats = %+v, want its own custom 200 generation", statsA)
	}
	if statsB.Count != len(knowledge.ChunkBody(body, paramsB)) || statsB.Strategy != "custom" {
		t.Fatalf("B stats = %+v, want its own custom 400 generation", statsB)
	}
	if statsA.Count == statsB.Count {
		t.Fatalf("A and B report the same chunk count %d; the body should produce different windows", statsA.Count)
	}
	if got := env.chunkParams(t, materialID)["chunk_size"]; got != float64(400) {
		t.Fatalf("persisted chunk_size = %v, want B's 400", got)
	}

	// Exact set equality between Qdrant and the current MySQL chunks.
	finalIDs := env.currentChunkIDs(t, materialID)
	points := env.scrollPoints(t, materialID)
	gotIDs := pointIDs(points)
	if !sameIDs(gotIDs, finalIDs) {
		t.Fatalf("vector ids = %v, MySQL chunk ids = %v; the sets must be equal", gotIDs, finalIDs)
	}
	if len(points) != len(finalIDs) {
		t.Fatalf("vector count = %d, MySQL chunk count = %d", len(points), len(finalIDs))
	}
	for _, p := range points {
		if p.Payload.MaterialID != materialID || p.Payload.ClassID != env.classA {
			t.Fatalf("point %d payload = %+v, want material %d class %d", p.ID, p.Payload, materialID, env.classA)
		}
		if p.Payload.ChunkID != p.ID {
			t.Fatalf("point %d payload chunk_id = %d", p.ID, p.Payload.ChunkID)
		}
	}
	for _, id := range initialIDs {
		if containsID(gotIDs, id) {
			t.Fatalf("generation-0 point %d survived", id)
		}
	}
	for _, id := range idsA {
		if containsID(gotIDs, id) {
			t.Fatalf("A's generation point %d survived", id)
		}
	}
	for _, status := range env.statuses(t, materialID) {
		if status != db.IndexIndexed {
			t.Fatalf("final chunk status = %q, want indexed", status)
		}
	}

	// The current unique content must be reachable through both branches with
	// a deterministic embedding query.
	hits, err := env.engine.Search(context.Background(), env.classA, search.Request{
		Query: "目标片段", Mode: search.ModeVector, TopK: 5,
	})
	if err != nil {
		t.Fatalf("vector search: %v", err)
	}
	hitCurrent(t, hits, finalIDs)
	hits, err = env.engine.HybridInMaterials(context.Background(), env.classA, []int{materialID}, "目标片段", 5)
	if err != nil {
		t.Fatalf("hybrid search: %v", err)
	}
	hitCurrent(t, hits, finalIDs)
}

func (e *reindexIntegrationEnv) statuses(t *testing.T, materialID int) []db.IndexStatus {
	t.Helper()
	chunks, err := db.ListChunksByMaterial(context.Background(), e.conn, materialID, e.classA, "")
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	out := make([]db.IndexStatus, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, c.IndexStatus)
	}
	return out
}

func hitCurrent(t *testing.T, hits []search.Hit, finalIDs []int64) {
	t.Helper()
	for _, h := range hits {
		if containsID(finalIDs, h.ChunkID) && strings.Contains(h.Text, "目标片段") {
			return
		}
	}
	t.Fatalf("no hit points at the current chunk containing the unique fragment; hits = %+v", hits)
}

func sameIDs(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestReindexIntegrationDeleteFailureKeepsOldGeneration is the failure-path
// integration check: a vector-delete failure must answer an error and leave the
// chunks and strategy untouched, and a later rebuild must recover.
func TestReindexIntegrationDeleteFailureKeepsOldGeneration(t *testing.T) {
	env := openReindexIntegrationEnv(t)
	body := strings.Repeat("丙", 900)
	materialID, _ := env.createMaterial(t, "回归-重建删除失败-集成", body)
	if err := env.indexer.IndexMaterial(context.Background(), materialID, env.classA); err != nil {
		t.Fatalf("initial index: %v", err)
	}
	beforeIDs := env.currentChunkIDs(t, materialID)
	beforeParams := env.chunkParams(t, materialID)

	// A vector store that is down for the first delete, then recovers. The
	// indexer still indexes through the real Qdrant, so the old points remain
	// until a successful rebuild deletes them.
	failing := &failOnceVectors{Vectors: env.vectors}
	paramsA := knowledge.ChunkParams{Strategy: knowledge.StrategyCustom, ChunkSize: 200, OverlapPercent: 10}
	if _, err := env.indexer.Reindex(context.Background(), ReindexRequest{
		MaterialID: materialID, ClassID: env.classA, Params: paramsA,
		Store: SQLReindexStore{Conn: env.conn}, Vectors: failing,
	}); err == nil {
		t.Fatal("reindex succeeded although deleting the vectors failed")
	}
	if got := env.currentChunkIDs(t, materialID); !sameIDs(got, beforeIDs) {
		t.Fatalf("chunks changed after a delete failure: %v -> %v", beforeIDs, got)
	}
	if got := env.chunkParams(t, materialID)["chunk_size"]; got != beforeParams["chunk_size"] {
		t.Fatalf("strategy changed after a delete failure: %v -> %v", beforeParams, env.chunkParams(t, materialID))
	}

	paramsB := knowledge.ChunkParams{Strategy: knowledge.StrategyCustom, ChunkSize: 400, OverlapPercent: 10}
	if _, err := env.indexer.Reindex(context.Background(), ReindexRequest{
		MaterialID: materialID, ClassID: env.classA, Params: paramsB,
		Store: SQLReindexStore{Conn: env.conn}, Vectors: failing,
	}); err != nil {
		t.Fatalf("the rebuild after a delete failure failed: %v", err)
	}
	points := env.scrollPoints(t, materialID)
	if !sameIDs(pointIDs(points), env.currentChunkIDs(t, materialID)) {
		t.Fatalf("final vector set does not match the current chunks")
	}
	for _, status := range env.statuses(t, materialID) {
		if status != db.IndexIndexed {
			t.Fatalf("final chunk status = %q, want indexed", status)
		}
	}
}

// failOnceVectors fails the first DeleteByMaterial call, then delegates.
type failOnceVectors struct {
	Vectors *vector.Client
	once    sync.Once
}

func (f *failOnceVectors) DeleteByMaterial(ctx context.Context, materialID, classID int) error {
	var err error
	f.once.Do(func() { err = fmt.Errorf("simulated vector-store outage") })
	if err != nil {
		return err
	}
	return f.Vectors.DeleteByMaterial(ctx, materialID, classID)
}
