package knowledge

import (
	"strings"
	"testing"
)

func hierarchyParams() ChunkParams {
	return ChunkParams{Strategy: StrategyHierarchy, ChunkSize: AutoSize, OverlapPercent: DefaultOverlapPercent}
}

// TestHierarchyPreambleAndTwoSections covers "两个一级标题得到前言 + 两章节的切片，
// 无切片区间同时覆盖两个标题行".
func TestHierarchyPreambleAndTwoSections(t *testing.T) {
	body := "前言内容。\n\n# 第一章\n第一章正文。\n\n# 第二章\n第二章正文。\n"
	runes := []rune(body)

	headings := headingOffsets(runes)
	if len(headings) != 2 {
		t.Fatalf("found %d headings at %v, want 2", len(headings), headings)
	}

	spans := HierarchySpans(runes)
	if len(spans) != 3 {
		t.Fatalf("got %d spans (%v), want preamble + 2 sections", len(spans), spans)
	}

	for i, s := range spans {
		covered := 0
		for _, h := range headings {
			if h >= s.Start && h < s.End {
				covered++
			}
		}
		if covered > 1 {
			t.Fatalf("span %d (%v) covers %d heading lines, want at most 1", i, s, covered)
		}
	}

	chunks := ChunkBody(body, hierarchyParams())
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks (%v), want 3", len(chunks), chunks)
	}
	if !strings.Contains(chunks[0].Text, "前言内容。") {
		t.Fatalf("chunk 0 = %q, want the preamble", chunks[0].Text)
	}
	if !strings.HasPrefix(chunks[1].Text, "# 第一章") {
		t.Fatalf("chunk 1 = %q, want the first section", chunks[1].Text)
	}
	if !strings.HasPrefix(chunks[2].Text, "# 第二章") {
		t.Fatalf("chunk 2 = %q, want the second section", chunks[2].Text)
	}
}

// TestHierarchyIgnoresHashInsideCodeFence covers "代码块里的 # x 不产生切分".
func TestHierarchyIgnoresHashInsideCodeFence(t *testing.T) {
	body := "# 第一章\n\n```go\n# 这不是标题\nfmt.Println(\"hi\")\n```\n\n# 第二章\n"
	runes := []rune(body)

	headings := headingOffsets(runes)
	if len(headings) != 2 {
		t.Fatalf("found %d headings at %v, want only the two real ones", len(headings), headings)
	}

	chunks := ChunkBody(body, hierarchyParams())
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2 (the fenced hash must not split)", len(chunks))
	}
	if !strings.Contains(chunks[0].Text, "# 这不是标题") {
		t.Fatalf("chunk 0 = %q, want the fenced line kept inside the first section", chunks[0].Text)
	}
}

func TestHierarchyClosedFenceIsFollowedByRealHeadings(t *testing.T) {
	body := "# 第一章\n\n```\n# 代码里的井号\n```\n\n# 第二章\n\n```\nno hash here\n```\n\n# 第三章\n"

	headings := headingOffsets([]rune(body))
	if len(headings) != 3 {
		t.Fatalf("found %d headings, want 3", len(headings))
	}
	if got := len(ChunkBody(body, hierarchyParams())); got != 3 {
		t.Fatalf("got %d chunks, want 3", got)
	}
}

// TestHierarchyLongSectionUsesAbsoluteOffsets covers "长章节的子片偏移为全文绝对偏移".
func TestHierarchyLongSectionUsesAbsoluteOffsets(t *testing.T) {
	body := "前言。\n" + "# 长章节\n" + strings.Repeat("字", 900)
	runes := []rune(body)

	spans := HierarchySpans(runes)

	want := []Span{{Start: 0, End: 4}, {Start: 4, End: 804}, {Start: 724, End: 910}}
	if len(spans) != len(want) {
		t.Fatalf("got %v, want %v", spans, want)
	}
	for i := range want {
		if spans[i] != want[i] {
			t.Fatalf("span %d = %v, want %v", i, spans[i], want[i])
		}
	}

	// The sub-spans must still select text from the right place in the body.
	if got := SpanText(runes, spans[1]); !strings.HasPrefix(got, "# 长章节\n") {
		t.Fatalf("first sub-span selects %q, want the section heading", got[:min(20, len(got))])
	}
	if spans[len(spans)-1].End != len(runes) {
		t.Fatalf("last sub-span ends at %d, want %d", spans[len(spans)-1].End, len(runes))
	}
}

func TestHierarchySectionAtWindowBoundaryStaysWhole(t *testing.T) {
	body := "# 章节\n" + strings.Repeat("字", AutoSize-5)
	if got := len([]rune(body)); got != AutoSize {
		t.Fatalf("fixture is %d characters, want %d", got, AutoSize)
	}

	spans := HierarchySpans([]rune(body))
	if len(spans) != 1 {
		t.Fatalf("got %v, want a single span for a section of exactly one window", spans)
	}
}

func TestHierarchyWithoutHeadingsFallsBackToAutoWindows(t *testing.T) {
	body := strings.Repeat("字", 2000)

	spans := HierarchySpans([]rune(body))
	want := []Span{{0, 800}, {720, 1520}, {1440, 2000}}
	if len(spans) != len(want) {
		t.Fatalf("got %v, want %v", spans, want)
	}
	for i := range want {
		if spans[i] != want[i] {
			t.Fatalf("span %d = %v, want %v", i, spans[i], want[i])
		}
	}
}

func TestHierarchyStartsWithHeadingHasNoPreambleChunk(t *testing.T) {
	body := "# 唯一的章节\n正文。"

	chunks := ChunkBody(body, hierarchyParams())
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks (%v), want 1", len(chunks), chunks)
	}
	if chunks[0].Span.Start != 0 {
		t.Fatalf("chunk starts at %d, want 0", chunks[0].Span.Start)
	}
}

func TestHierarchyAppliesPreprocessing(t *testing.T) {
	body := "# 章节\n详见 https://example.com/a 或发信到 a@example.com 。"

	params := hierarchyParams()
	params.RemoveURL = true
	params.RemoveEmail = true
	params.CollapseWhitespace = true

	chunks := ChunkBody(body, params)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if strings.Contains(chunks[0].Text, "http") || strings.Contains(chunks[0].Text, "@") {
		t.Fatalf("chunk text was not preprocessed: %q", chunks[0].Text)
	}
	if raw := SpanText([]rune(body), chunks[0].Span); !strings.Contains(raw, "https://example.com/a") {
		t.Fatalf("span no longer points at the original body: %q", raw)
	}
}

func TestIsHeadingLine(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"# 标题", true},
		{"## 二级", true},
		{"###### 六级", true},
		{"####### 七级", false},
		{"#标题", false},
		{"#", false},
		{"# ", false},
		{"#\t制表符", true},
		{"  # 缩进", false},
		{"正文 # 井号", false},
		{"", false},
		{"# 标题\r", true},
	} {
		if got := isHeadingLine([]rune(tc.line)); got != tc.want {
			t.Errorf("isHeadingLine(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestIsFenceLine(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"```", true},
		{"```go", true},
		{"   ```", true},
		{"    ```", false},
		{"~~", false},
		{"``", false},
		{"正文 ```", false},
	} {
		if got := isFenceLine([]rune(tc.line)); got != tc.want {
			t.Errorf("isFenceLine(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// TestHierarchyChunkBodyDispatchesByStrategy makes sure the window strategies
// still go through the window path.
func TestHierarchyChunkBodyDispatchesByStrategy(t *testing.T) {
	body := "# 标题\n" + strings.Repeat("字", 900)

	auto := ChunkBody(body, mustParams(t, nil))
	if len(auto) != 2 {
		t.Fatalf("auto produced %d chunks, want 2 windows", len(auto))
	}
	hierarchy := ChunkBody(body, mustParams(t, map[string]string{ParamStrategy: "hierarchy"}))
	if len(hierarchy) != 2 {
		t.Fatalf("hierarchy produced %d chunks, want 2 (section re-cut by auto)", len(hierarchy))
	}
	if auto[0].Span != hierarchy[0].Span {
		t.Fatalf("auto and hierarchy disagree on the first span: %v vs %v", auto[0].Span, hierarchy[0].Span)
	}
}
