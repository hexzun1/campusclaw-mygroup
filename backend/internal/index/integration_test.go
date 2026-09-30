package index

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/gateway"
	"campusclaw/backend/internal/vector"
)

// This test wires the real pieces together — MySQL, the stub gateway and
// Qdrant — because "the chunks end up indexed and searchable, or failed but
// still in MySQL" is a property of the whole pipeline, not of the indexer
// alone. Enable it with:
//
//	CAMPUSCLAW_TEST_DSN=app:pass@tcp(db:3306)/campusclaw?parseTime=true&charset=utf8mb4
//	CAMPUSCLAW_TEST_QDRANT_URL=http://qdrant:6333
//	CAMPUSCLAW_TEST_GATEWAY_URL=http://stubgateway:8090
//	CAMPUSCLAW_TEST_EMBEDDING_DIM=256

type pipelineEnv struct {
	conn      *sql.DB
	indexer   *Indexer
	qdrantURL string
	stubURL   string
	dim       int
	classA    int
	teacherA  int
}

func openPipeline(t *testing.T) *pipelineEnv {
	t.Helper()

	dsn := os.Getenv("CAMPUSCLAW_TEST_DSN")
	qdrantURL := os.Getenv("CAMPUSCLAW_TEST_QDRANT_URL")
	stubURL := os.Getenv("CAMPUSCLAW_TEST_GATEWAY_URL")
	if dsn == "" || qdrantURL == "" || stubURL == "" {
		t.Skip("CAMPUSCLAW_TEST_DSN, CAMPUSCLAW_TEST_QDRANT_URL and CAMPUSCLAW_TEST_GATEWAY_URL must all be set")
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

	env := &pipelineEnv{conn: conn, qdrantURL: qdrantURL, stubURL: stubURL, dim: dim}

	ctx := context.Background()
	if err := conn.QueryRowContext(ctx, `SELECT id FROM classes WHERE name = ?`, "A班").Scan(&env.classA); err != nil {
		t.Skipf("seeded class A missing (%v); is this a seeded database?", err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, "teacher_a").Scan(&env.teacherA); err != nil {
		t.Skipf("seeded teacher_a missing (%v); is this a seeded database?", err)
	}

	embedder := gateway.NewEmbeddingClient(gateway.EmbeddingConfig{
		BaseURL: stubURL, APIKey: "stub-embedding-key", Model: "stub-embedding",
		Dim: dim, Batch: 32, Timeout: 10 * time.Second,
	})
	vectors := vector.NewClient(qdrantURL, "", dim, 10*time.Second)
	env.indexer = New(SQLChunkStore{Conn: conn}, embedder, vectors, 32, 30*time.Second)
	return env
}

// createMaterialWithChunks inserts a material, its knowledge entry and count
// pending chunks, and registers cleanup for both MySQL and Qdrant.
func (e *pipelineEnv) createMaterialWithChunks(t *testing.T, title string, count int) (materialID int, chunks []db.Chunk) {
	t.Helper()
	ctx := context.Background()

	body := "正文用于索引测试"
	materialID, err := db.InsertMaterialWithKnowledge(ctx, e.conn, e.classA, title, "integration.md", title+".md", int64(len(body)), e.teacherA, body)
	if err != nil {
		t.Fatalf("insert material: %v", err)
	}

	var entryID int
	if err := e.conn.QueryRowContext(ctx, `SELECT id FROM knowledge_entries WHERE material_id = ?`, materialID).Scan(&entryID); err != nil {
		t.Fatalf("read knowledge entry: %v", err)
	}

	for i := 0; i < count; i++ {
		chunks = append(chunks, db.Chunk{
			KnowledgeEntryID: entryID,
			MaterialID:       materialID,
			ClassID:          e.classA,
			ChunkIndex:       i,
			CharStart:        i,
			CharEnd:          i + 1,
			ChunkText:        "第 " + strconv.Itoa(i) + " 段正文",
			IndexStatus:      db.IndexPending,
		})
	}
	if err := db.InsertChunks(ctx, e.conn, chunks); err != nil {
		t.Fatalf("insert chunks: %v", err)
	}

	// Read back so the test works with the real primary keys.
	stored, err := db.ListChunksByMaterial(ctx, e.conn, materialID, e.classA, "")
	if err != nil {
		t.Fatalf("list inserted chunks: %v", err)
	}

	t.Cleanup(func() {
		if _, err := e.conn.ExecContext(context.Background(), `DELETE FROM materials WHERE id = ?`, materialID); err != nil {
			t.Errorf("cleanup material %d: %v", materialID, err)
		}
		if err := vector.NewClient(e.qdrantURL, "", e.dim, 10*time.Second).
			DeleteByMaterial(context.Background(), materialID, e.classA); err != nil {
			t.Errorf("cleanup vectors of material %d: %v", materialID, err)
		}
	})
	return materialID, stored
}

func (e *pipelineEnv) statuses(t *testing.T, materialID int) []db.IndexStatus {
	t.Helper()
	chunks, err := db.ListChunksByMaterial(context.Background(), e.conn, materialID, e.classA, "")
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	statuses := make([]db.IndexStatus, 0, len(chunks))
	for _, c := range chunks {
		statuses = append(statuses, c.IndexStatus)
	}
	return statuses
}

func (e *pipelineEnv) countVectors(t *testing.T, materialID int) int {
	t.Helper()
	body := map[string]any{
		"exact": true,
		"filter": map[string]any{"must": []any{
			map[string]any{"key": "material_id", "match": map[string]any{"value": materialID}},
			map[string]any{"key": "class_id", "match": map[string]any{"value": e.classA}},
		}},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode count body: %v", err)
	}
	resp, err := http.Post(e.qdrantURL+"/collections/"+vector.CollectionName+"/points/count",
		"application/json", bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("count points: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var decoded struct {
		Result struct {
			Count int `json:"count"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode count response %s: %v", raw, err)
	}
	return decoded.Result.Count
}

func (e *pipelineEnv) setStubEmbeddingFailure(t *testing.T, failing bool) {
	t.Helper()
	encoded, err := json.Marshal(map[string]bool{"embed_fail": failing})
	if err != nil {
		t.Fatalf("encode control body: %v", err)
	}
	resp, err := http.Post(e.stubURL+"/control", "application/json", bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("POST /control: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /control status = %d, want 200", resp.StatusCode)
	}
}

func TestIndexPipelineIndexesEveryChunk(t *testing.T) {
	env := openPipeline(t)
	materialID, _ := env.createMaterialWithChunks(t, "回归-索引-正常", 3)

	if err := env.indexer.IndexMaterial(context.Background(), materialID, env.classA); err != nil {
		t.Fatalf("IndexMaterial failed: %v", err)
	}

	for i, status := range env.statuses(t, materialID) {
		if status != db.IndexIndexed {
			t.Fatalf("chunk %d status = %q, want indexed", i, status)
		}
	}
	if got := env.countVectors(t, materialID); got != 3 {
		t.Fatalf("the vector store holds %d points for the material, want 3", got)
	}
}

// TestIndexPipelineRecordsFailuresWithoutLosingRows is the second half of the
// verify from tasks.md 5.1.
func TestIndexPipelineRecordsFailuresWithoutLosingRows(t *testing.T) {
	env := openPipeline(t)
	materialID, _ := env.createMaterialWithChunks(t, "回归-索引-失败", 3)

	env.setStubEmbeddingFailure(t, true)
	t.Cleanup(func() { env.setStubEmbeddingFailure(t, false) })

	if err := env.indexer.IndexMaterial(context.Background(), materialID, env.classA); err != nil {
		t.Fatalf("IndexMaterial returned %v, want nil (the failure is recorded on the chunks)", err)
	}

	statuses := env.statuses(t, materialID)
	if len(statuses) != 3 {
		t.Fatalf("the material has %d chunks, want all 3 rows kept", len(statuses))
	}
	for i, status := range statuses {
		if status != db.IndexFailed {
			t.Fatalf("chunk %d status = %q, want failed", i, status)
		}
	}
	if got := env.countVectors(t, materialID); got != 0 {
		t.Fatalf("the vector store holds %d points, want none", got)
	}

	// The rows must still be searchable through the keyword path.
	hits, err := db.SearchChunksByKeyword(context.Background(), env.conn, env.classA, "段正文", 50)
	if err != nil {
		t.Fatalf("keyword search failed: %v", err)
	}
	found := false
	for _, h := range hits {
		if h.MaterialID == materialID {
			found = true
		}
	}
	if !found {
		t.Fatal("the failed material is not reachable through keyword search")
	}
}
