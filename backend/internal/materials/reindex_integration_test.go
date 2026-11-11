package materials

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
	"campusclaw/backend/internal/index"
	"campusclaw/backend/internal/knowledge"
	"campusclaw/backend/internal/vector"
)

// These handler tests exercise the rebuild against a real MySQL. Qdrant and
// the embedding gateway are replaced by in-memory fakes, so the test checks the
// request validation, the critical-section ordering and the response shape
// without needing those services. Enable with CAMPUSCLAW_TEST_DSN.

type handlerEmbedder struct{}

func (handlerEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i := range out {
		out[i] = []float64{1, 0}
	}
	return out, nil
}

type handlerVectors struct {
	mu sync.Mutex

	deleteErr  error
	deletes    int
	upserts    int
	pointCount int
}

func (v *handlerVectors) Upsert(_ context.Context, points []vector.Point) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.upserts++
	v.pointCount += len(points)
	return nil
}

func (v *handlerVectors) DeleteByMaterial(_ context.Context, _, _ int) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.deleteErr != nil {
		return v.deleteErr
	}
	v.deletes++
	return nil
}

type handlerFixture struct {
	conn     *sql.DB
	handlers *Handlers
	vectors  *handlerVectors
	classA   int
	classB   int
	teacherA int
}

func newHandlerFixture(t *testing.T) *handlerFixture {
	t.Helper()
	dsn := os.Getenv("CAMPUSCLAW_TEST_DSN")
	if dsn == "" {
		t.Skip("CAMPUSCLAW_TEST_DSN not set; skipping MySQL integration test")
	}
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping test db: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	f := &handlerFixture{conn: conn, vectors: &handlerVectors{}}
	ctx := context.Background()
	if err := conn.QueryRowContext(ctx, `SELECT id FROM classes WHERE name = ?`, "A班").Scan(&f.classA); err != nil {
		t.Skipf("seeded class A missing (%v); is this a seeded database?", err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT id FROM classes WHERE name = ?`, "B班").Scan(&f.classB); err != nil {
		t.Skipf("seeded class B missing (%v); is this a seeded database?", err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, "teacher_a").Scan(&f.teacherA); err != nil {
		t.Skipf("seeded teacher_a missing (%v); is this a seeded database?", err)
	}

	indexer := index.New(index.SQLChunkStore{Conn: conn}, handlerEmbedder{}, f.vectors, 32, 5*time.Second)
	f.handlers = NewHandlers(conn, indexer, f.vectors)
	return f
}

func (f *handlerFixture) createMaterial(t *testing.T, classID int, title, body string) int {
	t.Helper()
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
	materialID, err := db.InsertMaterialWithChunks(context.Background(), f.conn, db.NewMaterial{
		ClassID:       classID,
		Title:         title,
		StoredName:    title + ".md",
		OriginalName:  title + ".md",
		SizeBytes:     int64(len(body)),
		UploadedBy:    f.teacherA,
		BodyText:      body,
		ChunkStrategy: string(params.Strategy),
		ChunkParams:   params.ParamsJSON(),
		Chunks:        chunks,
	})
	if err != nil {
		t.Fatalf("insert material: %v", err)
	}
	t.Cleanup(func() {
		if _, err := f.conn.ExecContext(context.Background(), `DELETE FROM materials WHERE id = ?`, materialID); err != nil {
			t.Errorf("cleanup material %d: %v", materialID, err)
		}
	})
	return materialID
}

func (f *handlerFixture) chunkIDs(t *testing.T, materialID, classID int) []int64 {
	t.Helper()
	chunks, err := db.ListChunksByMaterial(context.Background(), f.conn, materialID, classID, "")
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	ids := make([]int64, 0, len(chunks))
	for _, c := range chunks {
		ids = append(ids, c.ID)
	}
	return ids
}

func (f *handlerFixture) request(t *testing.T, materialID int, body string, role db.Role, classID int) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/materials/%d/reindex", materialID), strings.NewReader(body))
	req = req.WithContext(httpapi.WithSessionUser(req.Context(), &db.SessionUser{
		UserID: f.teacherA, Username: "teacher_a", Role: role, ClassID: classID,
	}))
	req.SetPathValue("id", strconv.Itoa(materialID))
	return req
}

func TestReindexHandlerRebuildsAndReportsItsOwnStats(t *testing.T) {
	f := newHandlerFixture(t)
	body := strings.Repeat("甲", 900)
	materialID := f.createMaterial(t, f.classA, "回归-重建成功", body)
	beforeIDs := f.chunkIDs(t, materialID, f.classA)

	rec := httptest.NewRecorder()
	f.handlers.Reindex(rec, f.request(t, materialID, `{"strategy":"custom","chunk_size":200}`, db.RoleTeacher, f.classA))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		ID            int    `json:"id"`
		IndexStatus   string `json:"index_status"`
		ChunkCount    int    `json:"chunk_count"`
		ChunkStrategy string `json:"chunk_strategy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != materialID || resp.IndexStatus != "indexed" || resp.ChunkStrategy != "custom" {
		t.Fatalf("response = %+v", resp)
	}
	if want := len(knowledge.ChunkBody(body, knowledge.ChunkParams{Strategy: knowledge.StrategyCustom, ChunkSize: 200, OverlapPercent: 10})); resp.ChunkCount != want {
		t.Fatalf("chunk_count = %d, want %d", resp.ChunkCount, want)
	}
	afterIDs := f.chunkIDs(t, materialID, f.classA)
	if sameIDs(beforeIDs, afterIDs) {
		t.Fatal("the chunks were not replaced")
	}
	if f.vectors.deletes != 1 || f.vectors.pointCount != len(afterIDs) {
		t.Fatalf("vector delete/upsert = %d/%d, want 1/%d", f.vectors.deletes, f.vectors.pointCount, len(afterIDs))
	}
	for _, c := range f.chunks(t, materialID, f.classA) {
		if c.IndexStatus != db.IndexIndexed {
			t.Fatalf("chunk %d status = %q, want indexed", c.ID, c.IndexStatus)
		}
	}
}

func TestReindexHandlerRejectsInvalidParamsWithoutChanges(t *testing.T) {
	f := newHandlerFixture(t)
	materialID := f.createMaterial(t, f.classA, "回归-重建非法参数", strings.Repeat("乙", 900))
	beforeIDs := f.chunkIDs(t, materialID, f.classA)

	rec := httptest.NewRecorder()
	f.handlers.Reindex(rec, f.request(t, materialID, `{"strategy":"custom","chunk_size":50}`, db.RoleTeacher, f.classA))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if got := f.chunkIDs(t, materialID, f.classA); !sameIDs(got, beforeIDs) {
		t.Fatalf("invalid parameters changed the chunks: %v -> %v", beforeIDs, got)
	}
	if f.vectors.deletes != 0 || f.vectors.upserts != 0 {
		t.Fatal("invalid parameters touched the vector store")
	}
}

func TestReindexHandlerCrossClassIsTheSameNotFound(t *testing.T) {
	f := newHandlerFixture(t)
	f.createMaterial(t, f.classA, "回归-重建-本班", "A 班正文")
	otherID := f.createMaterial(t, f.classB, "回归-重建-他班", "B 班正文")
	beforeIDs := f.chunkIDs(t, otherID, f.classB)

	crossRec := httptest.NewRecorder()
	f.handlers.Reindex(crossRec, f.request(t, otherID, "", db.RoleTeacher, f.classA))
	missingRec := httptest.NewRecorder()
	f.handlers.Reindex(missingRec, f.request(t, 99999999, "", db.RoleTeacher, f.classA))

	if crossRec.Code != http.StatusNotFound || missingRec.Code != http.StatusNotFound {
		t.Fatalf("statuses = %d/%d, want 404/404", crossRec.Code, missingRec.Code)
	}
	if crossRec.Body.String() != missingRec.Body.String() {
		t.Fatalf("cross-class and missing bodies differ: %q vs %q", crossRec.Body.String(), missingRec.Body.String())
	}
	if got := f.chunkIDs(t, otherID, f.classB); !sameIDs(got, beforeIDs) {
		t.Fatalf("a cross-class request changed the other class's chunks: %v -> %v", beforeIDs, got)
	}
}

func TestReindexHandlerVectorDeleteFailureKeepsOldChunks(t *testing.T) {
	f := newHandlerFixture(t)
	materialID := f.createMaterial(t, f.classA, "回归-重建-向量删除失败", strings.Repeat("丙", 900))
	beforeIDs := f.chunkIDs(t, materialID, f.classA)
	f.vectors.deleteErr = errors.New("vector store is down")

	rec := httptest.NewRecorder()
	f.handlers.Reindex(rec, f.request(t, materialID, `{"strategy":"custom","chunk_size":400}`, db.RoleTeacher, f.classA))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", rec.Code, rec.Body.String())
	}
	if got := f.chunkIDs(t, materialID, f.classA); !sameIDs(got, beforeIDs) {
		t.Fatalf("a delete failure changed the chunks: %v -> %v", beforeIDs, got)
	}
	if f.vectors.upserts != 0 {
		t.Fatal("a delete failure wrote vectors")
	}
}

func (f *handlerFixture) chunks(t *testing.T, materialID, classID int) []db.Chunk {
	t.Helper()
	chunks, err := db.ListChunksByMaterial(context.Background(), f.conn, materialID, classID, "")
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	return chunks
}

func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
