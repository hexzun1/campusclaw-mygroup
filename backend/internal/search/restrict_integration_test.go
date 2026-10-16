package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/gateway"
	"campusclaw/backend/internal/index"
	"campusclaw/backend/internal/vector"
)

// These tests are the verify from tasks.md 4.1: hybrid retrieval restricted to
// a set of materials. They wire the real pieces together — MySQL, the stub
// gateway and a live Qdrant — and are enabled with the same variables as the
// other integration tests:
//
//	CAMPUSCLAW_TEST_DSN=... CAMPUSCLAW_TEST_QDRANT_URL=http://qdrant:6333
//	CAMPUSCLAW_TEST_GATEWAY_URL=http://stubgateway:8090
//	CAMPUSCLAW_TEST_EMBEDDING_DIM=...
//
// Every material a test creates is removed again from MySQL and Qdrant.

type restrictEnv struct {
	conn      *sql.DB
	engine    *Engine
	embedder  *gateway.EmbeddingClient
	vectors   *vector.Client
	indexer   *index.Indexer
	stubURL   string
	qdrantURL string
	classA    int
	classB    int
	teacherA  int
	dim       int
}

func openRestrictEnv(t *testing.T) *restrictEnv {
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

	env := &restrictEnv{conn: conn, stubURL: stubURL, qdrantURL: qdrantURL, dim: dim}
	ctx := context.Background()
	if err := conn.QueryRowContext(ctx, `SELECT id FROM classes WHERE name = ?`, "A班").Scan(&env.classA); err != nil {
		t.Skipf("seeded class A missing (%v); is this a seeded database?", err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT id FROM classes WHERE name = ?`, "B班").Scan(&env.classB); err != nil {
		t.Skipf("seeded class B missing (%v); is this a seeded database?", err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, "teacher_a").Scan(&env.teacherA); err != nil {
		t.Skipf("seeded teacher_a missing (%v); is this a seeded database?", err)
	}

	env.embedder = gateway.NewEmbeddingClient(gateway.EmbeddingConfig{
		BaseURL: stubURL, APIKey: "stub-embedding-key", Model: "stub-embedding",
		Dim: dim, Batch: 32, Timeout: 10 * time.Second,
	})
	env.vectors = vector.NewClient(qdrantURL, "", dim, 10*time.Second)
	env.indexer = index.New(index.SQLChunkStore{Conn: conn}, env.embedder, env.vectors, 32, 30*time.Second)
	env.engine = New(conn, env.embedder, env.vectors)
	return env
}

// newMaterial inserts one material with one chunk per text, indexes it and
// registers cleanup for both MySQL and Qdrant.
func (e *restrictEnv) newMaterial(t *testing.T, classID int, title string, chunkTexts []string) int {
	t.Helper()
	ctx := context.Background()

	body := ""
	for _, text := range chunkTexts {
		body += text + "\n"
	}
	materialID, err := db.InsertMaterialWithKnowledge(ctx, e.conn, classID, title, "restrict.md", title+".md", int64(len(body)), e.teacherA, body)
	if err != nil {
		t.Fatalf("insert material %q: %v", title, err)
	}

	var entryID int
	if err := e.conn.QueryRowContext(ctx, `SELECT id FROM knowledge_entries WHERE material_id = ?`, materialID).Scan(&entryID); err != nil {
		t.Fatalf("read knowledge entry of material %d: %v", materialID, err)
	}
	chunks := make([]db.Chunk, 0, len(chunkTexts))
	for i, text := range chunkTexts {
		chunks = append(chunks, db.Chunk{
			KnowledgeEntryID: entryID,
			MaterialID:       materialID,
			ClassID:          classID,
			ChunkIndex:       i,
			CharStart:        i,
			CharEnd:          i + 1,
			ChunkText:        text,
			IndexStatus:      db.IndexPending,
		})
	}
	if err := db.InsertChunks(ctx, e.conn, chunks); err != nil {
		t.Fatalf("insert chunks of material %d: %v", materialID, err)
	}

	t.Cleanup(func() {
		if _, err := e.conn.ExecContext(context.Background(), `DELETE FROM materials WHERE id = ?`, materialID); err != nil {
			t.Errorf("cleanup material %d: %v", materialID, err)
		}
		if err := vector.NewClient(e.qdrantURL, "", e.dim, 10*time.Second).
			DeleteByMaterial(context.Background(), materialID, classID); err != nil {
			t.Errorf("cleanup vectors of material %d: %v", materialID, err)
		}
	})

	if err := e.indexer.IndexMaterial(ctx, materialID, classID); err != nil {
		t.Fatalf("IndexMaterial(%d) failed: %v", materialID, err)
	}
	return materialID
}

// embeddingCalls reads the stub's embedding call counter, so "the empty set
// never reaches the gateway" can be asserted without guessing.
func (e *restrictEnv) embeddingCalls(t *testing.T) int {
	t.Helper()
	resp, err := http.Get(e.stubURL + "/stats")
	if err != nil {
		t.Fatalf("GET /stats: %v", err)
	}
	defer resp.Body.Close()
	var decoded struct {
		EmbeddingCalls int `json:"embedding_calls"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode /stats: %v", err)
	}
	return decoded.EmbeddingCalls
}

const restrictQuery = "二次函数"

func (e *restrictEnv) queryVector(t *testing.T) []float64 {
	t.Helper()
	vec, err := e.embedder.EmbedOne(context.Background(), restrictQuery)
	if err != nil {
		t.Fatalf("embed query: %v", err)
	}
	return vec
}

func hitMaterials(hits []Hit) []int {
	out := make([]int, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.MaterialID)
	}
	return out
}

// TestHybridInMaterialsRestrictsBothBranches: with the set {甲} both candidate
// branches exclude 乙 — while an unrestricted query proves both branches would
// have surfaced it.
func TestHybridInMaterialsRestrictsBothBranches(t *testing.T) {
	env := openRestrictEnv(t)
	ctx := context.Background()

	jia := env.newMaterial(t, env.classA, "回归-限定-甲", []string{"甲材料讲二次函数", "甲材料续讲二次函数的图像"})
	yi := env.newMaterial(t, env.classA, "回归-限定-乙", []string{"乙材料讲二次函数"})

	// Control: unrestricted, both branches return 乙.
	keywordHits, err := db.SearchChunksByKeyword(ctx, env.conn, env.classA, restrictQuery, db.KeywordLimit)
	if err != nil {
		t.Fatalf("unrestricted keyword search: %v", err)
	}
	if !keywordHitsInclude(keywordHits, yi) {
		t.Fatal("control failed: the unrestricted keyword branch does not contain 乙")
	}
	points, err := env.vectors.Search(ctx, env.classA, env.queryVector(t), vector.SearchLimit)
	if err != nil {
		t.Fatalf("unrestricted vector search: %v", err)
	}
	vectorIncludes := false
	for _, p := range points {
		if p.Payload.MaterialID == yi {
			vectorIncludes = true
		}
	}
	if !vectorIncludes {
		t.Fatal("control failed: the unrestricted vector branch does not contain 乙")
	}

	// The keyword branch restricted to {甲} never returns 乙.
	restrictedKeyword, err := db.SearchChunksByKeywordInMaterials(ctx, env.conn, env.classA, []int{jia}, restrictQuery, db.KeywordLimit)
	if err != nil {
		t.Fatalf("restricted keyword search: %v", err)
	}
	if len(restrictedKeyword) == 0 {
		t.Fatal("the restricted keyword branch returned nothing for 甲")
	}
	if keywordHitsInclude(restrictedKeyword, yi) {
		t.Fatal("the restricted keyword branch returned a chunk of 乙")
	}

	// The vector branch restricted to {甲} never returns 乙.
	restrictedPoints, err := env.vectors.SearchInMaterials(ctx, env.classA, []int{jia}, env.queryVector(t), vector.SearchLimit)
	if err != nil {
		t.Fatalf("restricted vector search: %v", err)
	}
	for _, p := range restrictedPoints {
		if p.Payload.MaterialID != jia {
			t.Fatalf("the restricted vector branch returned material %d, want only %d", p.Payload.MaterialID, jia)
		}
	}

	// The engine's fused result only carries 甲的 slices.
	hits, err := env.engine.HybridInMaterials(ctx, env.classA, []int{jia}, restrictQuery, 20)
	if err != nil {
		t.Fatalf("HybridInMaterials({甲}): %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("HybridInMaterials({甲}) returned nothing")
	}
	for _, h := range hits {
		if h.MaterialID != jia {
			t.Fatalf("HybridInMaterials({甲}) returned material %d", h.MaterialID)
		}
	}
	t.Logf("restricted {甲}: %d hits from materials %v", len(hits), hitMaterials(hits))
}

func keywordHitsInclude(hits []db.ChunkHit, materialID int) bool {
	for _, h := range hits {
		if h.MaterialID == materialID {
			return true
		}
	}
	return false
}

// TestHybridInMaterialsIntersectsTheClass: the set {A 班甲, B 班丙} can only
// ever answer 甲 — 丙 is a material of another class — while 丙 stays
// retrievable inside its own class.
func TestHybridInMaterialsIntersectsTheClass(t *testing.T) {
	env := openRestrictEnv(t)
	ctx := context.Background()

	jia := env.newMaterial(t, env.classA, "回归-限定-甲", []string{"甲材料讲二次函数"})
	bing := env.newMaterial(t, env.classB, "回归-限定-丙", []string{"丙材料讲二次函数"})

	// 丙 is retrievable within class B…
	bingHits, err := env.engine.HybridInMaterials(ctx, env.classB, []int{bing}, restrictQuery, 20)
	if err != nil {
		t.Fatalf("HybridInMaterials(class B, {丙}): %v", err)
	}
	found := false
	for _, h := range bingHits {
		if h.MaterialID == bing {
			found = true
		}
	}
	if !found {
		t.Fatal("control failed: 丙 is not retrievable inside class B")
	}

	// …but a class-A request that names it answers 甲 and only 甲.
	hits, err := env.engine.HybridInMaterials(ctx, env.classA, []int{jia, bing}, restrictQuery, 20)
	if err != nil {
		t.Fatalf("HybridInMaterials(class A, {甲, 丙}): %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("HybridInMaterials(class A, {甲, 丙}) returned nothing")
	}
	for _, h := range hits {
		if h.MaterialID != jia {
			t.Fatalf("HybridInMaterials(class A, {甲, 丙}) returned material %d, want only %d", h.MaterialID, jia)
		}
	}
	t.Logf("restricted {甲, 丙}: %d hits from materials %v", len(hits), hitMaterials(hits))
}

// TestHybridInMaterialsEmptySetSkipsTheGateway: an empty set — and a set whose
// every id is foreign or nonexistent — returns no results without ever calling
// the embedding gateway.
func TestHybridInMaterialsEmptySetSkipsTheGateway(t *testing.T) {
	env := openRestrictEnv(t)
	ctx := context.Background()

	env.newMaterial(t, env.classA, "回归-限定-甲", []string{"甲材料讲二次函数"})

	before := env.embeddingCalls(t)

	hits, err := env.engine.HybridInMaterials(ctx, env.classA, nil, restrictQuery, 20)
	if err != nil {
		t.Fatalf("HybridInMaterials(nil): %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("HybridInMaterials(nil) returned %d hits, want 0", len(hits))
	}

	hits, err = env.engine.HybridInMaterials(ctx, env.classA, []int{999999}, restrictQuery, 20)
	if err != nil {
		t.Fatalf("HybridInMaterials({999999}): %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("HybridInMaterials({999999}) returned %d hits, want 0", len(hits))
	}

	if after := env.embeddingCalls(t); after != before {
		t.Fatalf("embedding calls changed from %d to %d; the empty set must not reach the gateway", before, after)
	}
}
