package db

import (
	"context"
	"strings"
	"testing"
)

// keywordFixtureTexts use vocabulary that appears in exactly one class, so a
// keyword hit in the other class would be a class-isolation failure.
const (
	textA = "番茄炒蛋的做法很简单，先把鸡蛋打散，再下锅翻炒，最后加入番茄。"
	textB = "量子纠缠的实验需要低温环境，观测之前必须先制备纠缠光子对。"
)

func chunkWithText(entryID, materialID, classID, index int, text string) Chunk {
	return Chunk{
		KnowledgeEntryID: entryID,
		MaterialID:       materialID,
		ClassID:          classID,
		ChunkIndex:       index,
		CharStart:        0,
		CharEnd:          len([]rune(text)),
		ChunkText:        text,
	}
}

func assertAllClass(t *testing.T, hits []ChunkHit, classID int) {
	t.Helper()
	for _, h := range hits {
		if h.ClassID != classID {
			t.Fatalf("hit %d belongs to class %d, want class %d", h.ID, h.ClassID, classID)
		}
	}
}

// TestSearchChunksByKeywordAcrossClasses covers the class filter of the keyword
// path (spec: 关键词检索).
func TestSearchChunksByKeywordAcrossClasses(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, classB, teacherA := fixture(t, conn)

	matA, entryA := createTestMaterial(t, conn, classA, teacherA, "回归-关键词-A", textA)
	matB, entryB := createTestMaterial(t, conn, classB, teacherA, "回归-关键词-B", textB)

	if err := InsertChunks(ctx, conn, []Chunk{
		chunkWithText(entryA, matA, classA, 0, textA),
		chunkWithText(entryB, matB, classB, 0, textB),
	}); err != nil {
		t.Fatalf("insert keyword chunks: %v", err)
	}

	// A word that only exists in class B must not be visible to class A.
	hits, err := SearchChunksByKeyword(ctx, conn, classA, "量子纠缠", KeywordLimit)
	if err != nil {
		t.Fatalf("keyword search (foreign word): %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("class A saw %d hits for a class-B-only word", len(hits))
	}

	// A word from class A is found, belongs to class A, and is traceable.
	hits, err = SearchChunksByKeyword(ctx, conn, classA, "番茄炒蛋", KeywordLimit)
	if err != nil {
		t.Fatalf("keyword search (own word): %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("class A found no hits for a word in its own material")
	}
	assertAllClass(t, hits, classA)
	if !strings.Contains(hits[0].ChunkText, "番茄") {
		t.Fatalf("hit text %q does not contain the query word", hits[0].ChunkText)
	}
	if hits[0].MaterialTitle != "回归-关键词-A" {
		t.Fatalf("hit title = %q, want the joined material title", hits[0].MaterialTitle)
	}
	if hits[0].Score <= 0 {
		t.Fatalf("full-text score = %v, want a positive relevance", hits[0].Score)
	}

	// The mirror image: class B must not see class A's word.
	hits, err = SearchChunksByKeyword(ctx, conn, classB, "番茄炒蛋", KeywordLimit)
	if err != nil {
		t.Fatalf("keyword search (class B): %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("class B saw %d hits for a class-A-only word", len(hits))
	}
}

// TestSearchChunksByKeywordSeededMaterials checks the keyword path against the
// preset materials, which every environment has.
func TestSearchChunksByKeywordSeededMaterials(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, classB, _ := fixture(t, conn)

	// Chunk the seeded preset materials by hand: the compensation scan that
	// normally does this arrives in a later task.
	var seededA []int
	rows, err := conn.QueryContext(ctx,
		`SELECT m.id FROM materials m WHERE m.class_id = ? AND m.title LIKE '%A 班%'`, classA)
	if err != nil {
		t.Fatalf("find seeded class A material: %v", err)
	}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan seeded material: %v", err)
		}
		seededA = append(seededA, id)
	}
	rows.Close()
	if len(seededA) == 0 {
		t.Skip("no seeded class A material found")
	}

	for _, materialID := range seededA {
		var entryID int
		var body string
		if err := conn.QueryRowContext(ctx,
			`SELECT id, body_text FROM knowledge_entries WHERE material_id = ?`, materialID).Scan(&entryID, &body); err != nil {
			t.Fatalf("read seeded entry: %v", err)
		}
		existing, err := ListChunksByMaterial(ctx, conn, materialID, classA, "")
		if err != nil {
			t.Fatalf("list seeded chunks: %v", err)
		}
		if len(existing) > 0 {
			continue
		}
		if err := InsertChunks(ctx, conn, []Chunk{chunkWithText(entryID, materialID, classA, 0, body)}); err != nil {
			t.Fatalf("chunk seeded material: %v", err)
		}
		t.Cleanup(func() {
			if _, err := conn.ExecContext(context.Background(),
				`DELETE FROM knowledge_chunks WHERE material_id = ?`, materialID); err != nil {
				t.Errorf("cleanup seeded chunks for %d: %v", materialID, err)
			}
		})
	}

	hits, err := SearchChunksByKeyword(ctx, conn, classA, "材料", KeywordLimit)
	if err != nil {
		t.Fatalf("keyword search on seeded material: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no keyword hit for a two-character word present in the seeded class A material")
	}
	assertAllClass(t, hits, classA)

	// The seeded bodies are identical apart from the class letter, so the only
	// class-distinguishing token is that single letter. Reaching the other
	// class's chunk here would mean the LIKE fallback lost its class filter.
	hits, err = SearchChunksByKeyword(ctx, conn, classA, "B", KeywordLimit)
	if err != nil {
		t.Fatalf("single-character search for class A: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("class A saw %d hits for the letter that only class B's material carries", len(hits))
	}

	hits, err = SearchChunksByKeyword(ctx, conn, classB, "A", KeywordLimit)
	if err != nil {
		t.Fatalf("single-character search for class B: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("class B saw %d hits for the letter that only class A's material carries", len(hits))
	}
}

// TestSearchChunksByKeywordSingleCharacter covers the ngram fallback: the
// default ngram token size is 2, so a one-character query must go through LIKE
// and must still respect the class boundary.
func TestSearchChunksByKeywordSingleCharacter(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, classB, teacherA := fixture(t, conn)

	matA, entryA := createTestMaterial(t, conn, classA, teacherA, "回归-单字-A", textA)
	matB, entryB := createTestMaterial(t, conn, classB, teacherA, "回归-单字-B", textB)

	if err := InsertChunks(ctx, conn, []Chunk{
		chunkWithText(entryA, matA, classA, 0, textA),
		chunkWithText(entryB, matB, classB, 0, textB),
	}); err != nil {
		t.Fatalf("insert single-character chunks: %v", err)
	}

	hits, err := SearchChunksByKeyword(ctx, conn, classA, "番", KeywordLimit)
	if err != nil {
		t.Fatalf("single-character search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("single-character LIKE fallback found nothing in its own class")
	}
	assertAllClass(t, hits, classA)

	// 量 exists only in class B's text, so class A must get nothing.
	hits, err = SearchChunksByKeyword(ctx, conn, classA, "量", KeywordLimit)
	if err != nil {
		t.Fatalf("single-character search for a foreign character: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("class A saw %d hits for a character that only exists in class B", len(hits))
	}
}

// TestSearchChunksByKeywordEscapesLikeWildcards makes sure % and _ are matched
// literally instead of acting as wildcards.
func TestSearchChunksByKeywordEscapesLikeWildcards(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	classA, _, teacherA := fixture(t, conn)

	body := "百分比写作 50% 是合格的，下划线 _ 不是通配符。"
	matA, entryA := createTestMaterial(t, conn, classA, teacherA, "回归-转义-A", body)
	// A decoy chunk in the same class that carries no wildcard character: if
	// escaping regressed, the wildcard query would match it and the assertion
	// below would notice.
	decoyText := "这一段没有任何百分号或下划线，只用来验证通配符确实被转义。"
	matDecoy, entryDecoy := createTestMaterial(t, conn, classA, teacherA, "回归-转义-诱饵", decoyText)

	if err := InsertChunks(ctx, conn, []Chunk{
		chunkWithText(entryA, matA, classA, 0, body),
		chunkWithText(entryDecoy, matDecoy, classA, 0, decoyText),
	}); err != nil {
		t.Fatalf("insert escape fixture: %v", err)
	}

	for _, tc := range []struct{ query, literal string }{
		{"%", "%"},
		{"_", "_"},
	} {
		hits, err := SearchChunksByKeyword(ctx, conn, classA, tc.query, KeywordLimit)
		if err != nil {
			t.Fatalf("escaped %q search: %v", tc.query, err)
		}
		if len(hits) == 0 {
			t.Fatalf("escaped %q found nothing although a chunk contains it", tc.query)
		}
		for _, h := range hits {
			if !strings.Contains(h.ChunkText, tc.literal) {
				t.Fatalf("unescaped %q matched a chunk without that character: %q", tc.query, h.ChunkText)
			}
		}
	}
}
