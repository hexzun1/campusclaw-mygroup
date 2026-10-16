package tutor

import (
	"strings"
	"testing"

	"campusclaw/backend/internal/search"
)

func hit(materialID int, title string, chunkID int64, chunkIndex int, text string) search.Hit {
	return search.Hit{
		MaterialID:    materialID,
		MaterialTitle: title,
		ChunkID:       chunkID,
		ChunkIndex:    chunkIndex,
		CharStart:     chunkIndex * 100,
		CharEnd:       chunkIndex*100 + 99,
		Excerpt:       search.Excerpt(text),
		Score:         1,
		Text:          text,
	}
}

func TestBuildCitationsDropsEmptyBodies(t *testing.T) {
	hits := []search.Hit{
		hit(1, "数学一", 11, 0, "有正文"),
		hit(1, "数学一", 12, 1, ""),
		hit(1, "数学一", 13, 2, "   \n\t "),
		hit(1, "数学一", 14, 3, "又有正文"),
	}

	got := BuildCitations(hits, "")
	if len(got) != 2 {
		t.Fatalf("BuildCitations kept %d hits, want 2", len(got))
	}
	if got[0].ChunkID != 11 || got[1].ChunkID != 14 {
		t.Errorf("kept chunks = %d, %d; want 11, 14", got[0].ChunkID, got[1].ChunkID)
	}
}

func TestBuildCitationsRenumbersAfterSubjectFilter(t *testing.T) {
	// 检索前 4 条中有 1 条来自标题不含「数学」的材料：剔除后其余 3 条必须依
	// 次编号为 1、2、3，且顺序与检索顺序一致。
	hits := []search.Hit{
		hit(1, "数学错题集", 11, 0, "第一条"),
		hit(2, "语文阅读材料", 21, 0, "第二条（越界）"),
		hit(3, "数学公式表", 31, 2, "第三条"),
		hit(4, "高一数学练习", 41, 5, "第四条"),
	}

	got := BuildCitations(hits, "数学")
	if len(got) != 3 {
		t.Fatalf("BuildCitations kept %d hits, want 3", len(got))
	}
	for i, source := range got {
		if source.Index != i+1 {
			t.Errorf("index[%d] = %d, want %d", i, source.Index, i+1)
		}
	}
	if ids := []int64{got[0].ChunkID, got[1].ChunkID, got[2].ChunkID}; ids[0] != 11 || ids[1] != 31 || ids[2] != 41 {
		t.Errorf("kept chunks = %v, want [11 31 41]", ids)
	}
}

func TestBuildCitationsKeepsOrderAndFields(t *testing.T) {
	hits := []search.Hit{
		hit(2, "语文", 21, 1, "正文甲"),
		hit(1, "数学", 11, 3, "正文乙"),
	}

	got := BuildCitations(hits, "")
	if len(got) != 2 {
		t.Fatalf("BuildCitations kept %d hits, want 2", len(got))
	}
	first := got[0]
	if first.Index != 1 || first.MaterialID != 2 || first.MaterialTitle != "语文" || first.ChunkID != 21 || first.ChunkIndex != 1 {
		t.Errorf("first citation = %+v, want the first hit with index 1", first)
	}
	if first.CharStart != 100 || first.CharEnd != 199 {
		t.Errorf("char range = %d–%d, want 100–199", first.CharStart, first.CharEnd)
	}
	if first.Text != "正文甲" {
		t.Errorf("Text = %q, want the full body server-side", first.Text)
	}
}

func TestBuildCitationsExcerptTruncatesAt300(t *testing.T) {
	long := strings.Repeat("字", search.ExcerptChars+50)
	got := BuildCitations([]search.Hit{hit(1, "数学", 11, 0, long)}, "")
	if len(got) != 1 {
		t.Fatalf("BuildCitations kept %d hits, want 1", len(got))
	}
	want := strings.Repeat("字", search.ExcerptChars) + "…"
	if got[0].Excerpt != want {
		t.Errorf("excerpt is %d runes, want the 300-char truncation", len([]rune(got[0].Excerpt)))
	}
	if got[0].Text != long {
		t.Error("Text must stay untruncated for the gateway message")
	}
}

func TestBuildCitationsEmptyInputIsNeverNil(t *testing.T) {
	if got := BuildCitations(nil, "数学"); got == nil || len(got) != 0 {
		t.Errorf("BuildCitations(nil) = %v, want a non-nil empty slice", got)
	}
}
