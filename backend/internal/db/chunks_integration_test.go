package db

import (
	"context"
	"database/sql"
	"os"
	"testing"
)

// These tests exercise the chunk layer against a real MySQL, because the
// guarantees under test (class-filtered JOIN, transaction replacement,
// aggregate status) are enforced by SQL and by the schema. They are skipped
// unless CAMPUSCLAW_TEST_DSN points at a throwaway or development database,
// e.g.
//
//	CAMPUSCLAW_TEST_DSN='app:pass@tcp(db:3306)/campusclaw?parseTime=true&charset=utf8mb4'
//
// Every fixture row the test creates is removed again, so the seeded data of
// the target database is left as it was.

func openTestDB(t *testing.T) *sql.DB {
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
	return conn
}

// fixture returns the seeded class ids and a user id to attribute test
// materials to.
func fixture(t *testing.T, conn *sql.DB) (classA, classB, teacherA int) {
	t.Helper()
	ctx := context.Background()

	if err := conn.QueryRowContext(ctx, `SELECT id FROM classes WHERE name = ?`, "A班").Scan(&classA); err != nil {
		t.Skipf("seeded class A missing (%v); is this a seeded database?", err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT id FROM classes WHERE name = ?`, "B班").Scan(&classB); err != nil {
		t.Skipf("seeded class B missing (%v); is this a seeded database?", err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, "teacher_a").Scan(&teacherA); err != nil {
		t.Skipf("seeded teacher_a missing (%v); is this a seeded database?", err)
	}
	return classA, classB, teacherA
}

// createTestMaterial inserts a material with its knowledge entry and registers
// cleanup so nothing survives the test.
func createTestMaterial(t *testing.T, conn *sql.DB, classID, uploadedBy int, title, body string) (materialID, entryID int) {
	t.Helper()
	ctx := context.Background()

	materialID, err := InsertMaterialWithKnowledge(ctx, conn, classID, title, "test-stored.md", title+".md", int64(len(body)), uploadedBy, body)
	if err != nil {
		t.Fatalf("insert test material: %v", err)
	}
	t.Cleanup(func() {
		// knowledge_entries and knowledge_chunks cascade from materials.
		if _, err := conn.ExecContext(context.Background(), `DELETE FROM materials WHERE id = ?`, materialID); err != nil {
			t.Errorf("cleanup material %d: %v", materialID, err)
		}
	})

	if err := conn.QueryRowContext(ctx, `SELECT id FROM knowledge_entries WHERE material_id = ?`, materialID).Scan(&entryID); err != nil {
		t.Fatalf("read test knowledge entry: %v", err)
	}
	return materialID, entryID
}

func testChunks(entryID, materialID, classID int, count int) []Chunk {
	chunks := make([]Chunk, 0, count)
	for i := 0; i < count; i++ {
		chunks = append(chunks, Chunk{
			KnowledgeEntryID: entryID,
			MaterialID:       materialID,
			ClassID:          classID,
			ChunkIndex:       i,
			CharStart:        i * 10,
			CharEnd:          i*10 + 10,
			ChunkText:        "chunk body",
		})
	}
	return chunks
}

func chunkIDs(chunks []Chunk) []int64 {
	ids := make([]int64, 0, len(chunks))
	for _, c := range chunks {
		ids = append(ids, c.ID)
	}
	return ids
}

func setChunkClass(t *testing.T, conn *sql.DB, chunkID int64, classID int) {
	t.Helper()
	if _, err := conn.ExecContext(context.Background(),
		`UPDATE knowledge_chunks SET class_id = ? WHERE id = ?`, classID, chunkID); err != nil {
		t.Fatalf("relabel chunk %d: %v", chunkID, err)
	}
}

// TestGetChunksByIDsRejectsOtherClass is the class-isolation guarantee of the
// vector lookup path (spec: 向量检索 — 回表再次按班级过滤).
func TestGetChunksByIDsRejectsOtherClass(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, classB, teacherA := fixture(t, conn)

	matA, entryA := createTestMaterial(t, conn, classA, teacherA, "回归-切片-A", "A 班正文")
	matB, entryB := createTestMaterial(t, conn, classB, teacherA, "回归-切片-B", "B 班正文")

	if err := InsertChunks(ctx, conn, testChunks(entryA, matA, classA, 2)); err != nil {
		t.Fatalf("insert A chunks: %v", err)
	}
	if err := InsertChunks(ctx, conn, testChunks(entryB, matB, classB, 2)); err != nil {
		t.Fatalf("insert B chunks: %v", err)
	}

	chunksA, err := ListChunksByMaterial(ctx, conn, matA, classA, "")
	if err != nil {
		t.Fatalf("list A chunks: %v", err)
	}
	chunksB, err := ListChunksByMaterial(ctx, conn, matB, classB, "")
	if err != nil {
		t.Fatalf("list B chunks: %v", err)
	}
	if len(chunksA) != 2 || len(chunksB) != 2 {
		t.Fatalf("chunk counts = %d/%d, want 2/2", len(chunksA), len(chunksB))
	}

	// Class A asking for class B's chunk ids must get nothing at all.
	got, err := GetChunksByIDs(ctx, conn, classA, chunkIDs(chunksB))
	if err != nil {
		t.Fatalf("GetChunksByIDs(B ids, class A): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("GetChunksByIDs(B ids, class A) returned %d rows, want 0", len(got))
	}

	// The legitimate direction still works and carries the material title.
	got, err = GetChunksByIDs(ctx, conn, classA, chunkIDs(chunksA))
	if err != nil {
		t.Fatalf("GetChunksByIDs(A ids, class A): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetChunksByIDs(A ids, class A) returned %d rows, want 2", len(got))
	}
	if got[0].MaterialTitle != "回归-切片-A" {
		t.Fatalf("MaterialTitle = %q, want the joined title", got[0].MaterialTitle)
	}

	// A chunk row mislabelled as another class must not leak either.
	setChunkClass(t, conn, chunksA[0].ID, classB)
	got, err = GetChunksByIDs(ctx, conn, classA, []int64{chunksA[0].ID})
	if err != nil {
		t.Fatalf("GetChunksByIDs(mislabelled chunk): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a chunk row relabelled to class B was still returned to class A")
	}
	setChunkClass(t, conn, chunksA[0].ID, classA)

	// A chunk pointing at a class-B material must not leak into class A even
	// when the chunk row itself claims class A: the JOIN is what stops it.
	setChunkClass(t, conn, chunksB[0].ID, classA)
	got, err = GetChunksByIDs(ctx, conn, classA, []int64{chunksB[0].ID})
	if err != nil {
		t.Fatalf("GetChunksByIDs(foreign material): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a chunk of a class-B material was returned to class A")
	}
}

// TestReplaceChunksForMaterialRemovesOldRows covers "按材料替换" (spec: 按新策略重建).
func TestReplaceChunksForMaterialRemovesOldRows(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, _, teacherA := fixture(t, conn)

	matA, entryA := createTestMaterial(t, conn, classA, teacherA, "回归-重建-A", "A 班正文用于重建")

	if err := InsertChunks(ctx, conn, testChunks(entryA, matA, classA, 3)); err != nil {
		t.Fatalf("insert initial chunks: %v", err)
	}
	old, err := ListChunksByMaterial(ctx, conn, matA, classA, "")
	if err != nil {
		t.Fatalf("list initial chunks: %v", err)
	}
	if len(old) != 3 {
		t.Fatalf("initial chunks = %d, want 3", len(old))
	}

	replacement := testChunks(entryA, matA, classA, 2)
	if err := ReplaceChunksForMaterial(ctx, conn, matA, classA, "custom", []byte(`{"chunk_size":200}`), replacement); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}

	// Every old id must be gone, not merely unreferenced.
	for _, id := range chunkIDs(old) {
		var count int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_chunks WHERE id = ?`, id).Scan(&count); err != nil {
			t.Fatalf("count old chunk %d: %v", id, err)
		}
		if count != 0 {
			t.Fatalf("old chunk %d still exists after replacement", id)
		}
	}

	fresh, err := ListChunksByMaterial(ctx, conn, matA, classA, "")
	if err != nil {
		t.Fatalf("list replaced chunks: %v", err)
	}
	if len(fresh) != 2 {
		t.Fatalf("replaced chunks = %d, want 2", len(fresh))
	}
	for _, c := range fresh {
		if c.IndexStatus != IndexPending {
			t.Fatalf("replaced chunk status = %q, want pending", c.IndexStatus)
		}
	}

	stats, err := GetChunkStats(ctx, conn, matA, classA)
	if err != nil {
		t.Fatalf("chunk stats: %v", err)
	}
	if stats.Count != 2 || stats.Status != IndexPending || stats.Strategy != "custom" {
		t.Fatalf("stats = %+v, want {2 pending custom}", stats)
	}
}

// TestChunkStatsAggregationAndStatusUpdates covers the aggregate rule and the
// status writer used by the indexer.
func TestChunkStatsAggregationAndStatusUpdates(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, _, teacherA := fixture(t, conn)

	matA, entryA := createTestMaterial(t, conn, classA, teacherA, "回归-状态-A", "A 班正文用于状态")
	if err := InsertChunks(ctx, conn, testChunks(entryA, matA, classA, 3)); err != nil {
		t.Fatalf("insert chunks: %v", err)
	}

	// An entry with no chunks yet reports pending and zero count.
	emptyMat, _ := createTestMaterial(t, conn, classA, teacherA, "回归-空切片-A", "还没有切片")
	stats, err := GetChunkStats(ctx, conn, emptyMat, classA)
	if err != nil {
		t.Fatalf("stats for unchunked material: %v", err)
	}
	if stats.Count != 0 || stats.Status != IndexPending {
		t.Fatalf("unchunked stats = %+v, want {0 pending}", stats)
	}

	chunks, err := ListChunksByMaterial(ctx, conn, matA, classA, "")
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}

	if err := UpdateChunksStatus(ctx, conn, chunkIDs(chunks), IndexIndexed); err != nil {
		t.Fatalf("mark indexed: %v", err)
	}
	stats, err = GetChunkStats(ctx, conn, matA, classA)
	if err != nil {
		t.Fatalf("stats after indexed: %v", err)
	}
	if stats.Status != IndexIndexed || stats.Count != 3 {
		t.Fatalf("stats after indexed = %+v, want {3 indexed}", stats)
	}

	// One failed chunk must dominate one pending chunk.
	if err := UpdateChunksStatus(ctx, conn, []int64{chunks[0].ID}, IndexPending); err != nil {
		t.Fatalf("mark pending: %v", err)
	}
	if stats, _ = GetChunkStats(ctx, conn, matA, classA); stats.Status != IndexPending {
		t.Fatalf("stats with a pending chunk = %+v, want pending", stats)
	}
	if err := UpdateChunksStatus(ctx, conn, []int64{chunks[1].ID}, IndexFailed); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if stats, _ = GetChunkStats(ctx, conn, matA, classA); stats.Status != IndexFailed {
		t.Fatalf("stats with a failed chunk = %+v, want failed", stats)
	}

	pending, err := ListChunksByMaterial(ctx, conn, matA, classA, IndexPending)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != chunks[0].ID {
		t.Fatalf("pending filter returned %d rows, want the single pending chunk", len(pending))
	}
}

// TestReplaceChunksForMaterialRejectsForeignClass checks that a replacement
// aimed at the wrong class changes nothing.
func TestReplaceChunksForMaterialRejectsForeignClass(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, classB, teacherA := fixture(t, conn)

	matA, entryA := createTestMaterial(t, conn, classA, teacherA, "回归-错班替换-A", "A 班正文")
	if err := InsertChunks(ctx, conn, testChunks(entryA, matA, classA, 2)); err != nil {
		t.Fatalf("insert chunks: %v", err)
	}
	before, err := ListChunksByMaterial(ctx, conn, matA, classA, "")
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}

	err = ReplaceChunksForMaterial(ctx, conn, matA, classB, "auto", nil, testChunks(entryA, matA, classB, 1))
	if err == nil {
		t.Fatal("replacing a class-A material through class B succeeded, want an error")
	}

	after, err := ListChunksByMaterial(ctx, conn, matA, classA, "")
	if err != nil {
		t.Fatalf("list chunks after failed replace: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("chunk count changed from %d to %d after a rejected replacement", len(before), len(after))
	}
}
