package knowledge

import (
	"strings"
	"testing"
)

func mustParams(t *testing.T, raw map[string]string) ChunkParams {
	t.Helper()
	params, err := ParseParams(raw)
	if err != nil {
		t.Fatalf("ParseParams(%v) failed: %v", raw, err)
	}
	return params
}

func TestPreprocessOrderAndFlags(t *testing.T) {
	text := "看 https://example.com/a?b=1 或写信给 teacher@example.com   了解详情。"

	for _, tc := range []struct {
		name   string
		params ChunkParams
		want   string
	}{
		{
			name:   "全部关闭时原样返回",
			params: ChunkParams{},
			want:   text,
		},
		{
			name:   "只移除 URL",
			params: ChunkParams{RemoveURL: true},
			want:   "看  或写信给 teacher@example.com   了解详情。",
		},
		{
			name:   "只移除邮箱",
			params: ChunkParams{RemoveEmail: true},
			want:   "看 https://example.com/a?b=1 或写信给    了解详情。",
		},
		{
			name:   "只折叠空白",
			params: ChunkParams{CollapseWhitespace: true},
			want:   "看 https://example.com/a?b=1 或写信给 teacher@example.com 了解详情。",
		},
		{
			name:   "三者全开",
			params: ChunkParams{RemoveURL: true, RemoveEmail: true, CollapseWhitespace: true},
			want:   "看 或写信给 了解详情。",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Preprocess(text, tc.params); got != tc.want {
				t.Fatalf("Preprocess = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestChunkTextIsPreprocessedButSpanPointsAtTheBody is the traceability
// guarantee: the body is never rewritten, and each span still selects exactly
// the characters it selected before preprocessing ran.
func TestChunkTextIsPreprocessedButSpanPointsAtTheBody(t *testing.T) {
	body := "参考资料见 https://example.com/doc 或联系 help@example.com 。\n\n\n" +
		"第二段有   多余空白，也有一条链接 http://a.example.org/x 。"
	original := body

	params := mustParams(t, map[string]string{
		ParamStrategy:           "custom",
		ParamChunkSize:          "2000",
		ParamOverlapPercent:     "0",
		ParamRemoveURL:          "true",
		ParamRemoveEmail:        "true",
		ParamCollapseWhitespace: "true",
	})

	chunks := WindowChunks(body, params)
	if len(chunks) == 0 {
		t.Fatal("no chunks produced")
	}

	runes := []rune(original)
	for i, c := range chunks {
		if c.Index != i {
			t.Fatalf("chunk %d has index %d, want contiguous indices", i, c.Index)
		}
		if strings.Contains(c.Text, "http") {
			t.Fatalf("chunk %d text still contains a URL: %q", i, c.Text)
		}
		if strings.Contains(c.Text, "@") {
			t.Fatalf("chunk %d text still contains an e-mail address: %q", i, c.Text)
		}
		if strings.Contains(c.Text, "\n") {
			t.Fatalf("chunk %d text still contains a newline: %q", i, c.Text)
		}

		// The raw window is the untouched body slice; preprocessing produced
		// Text from it and changed nothing else.
		raw := SpanText(runes, c.Span)
		if !strings.Contains(raw, "http") && !strings.Contains(raw, "@") {
			t.Fatalf("chunk %d span does not select the original text: %q", i, raw)
		}
		want := Preprocess(raw, params)
		if c.Text != want {
			t.Fatalf("chunk %d text = %q, want Preprocess(raw) = %q", i, c.Text, want)
		}
	}

	if body != original {
		t.Fatalf("the body string was modified:\n got %q\nwant %q", body, original)
	}
}

// TestBlankChunksAreDroppedAndRenumbered covers "预处理后为空的切片丢弃，序号重新
// 连续编号".
func TestBlankChunksAreDroppedAndRenumbered(t *testing.T) {
	// With a 100-character window and no overlap this is exactly three windows:
	// 100 spaces, 50 spaces + 50 characters, 100 characters.
	body := strings.Repeat(" ", 150) + strings.Repeat("字", 150)

	params := mustParams(t, map[string]string{
		ParamStrategy:       "custom",
		ParamChunkSize:      "100",
		ParamOverlapPercent: "0",
	})

	chunks := WindowChunks(body, params)

	if len(chunks) != 2 {
		t.Fatalf("got %d chunks (%v), want 2 after dropping the blank window", len(chunks), chunks)
	}
	for i, c := range chunks {
		if c.Index != i {
			t.Fatalf("chunk %d has index %d, want contiguous indices after renumbering", i, c.Index)
		}
		if strings.TrimSpace(c.Text) == "" {
			t.Fatalf("chunk %d is blank: %q", i, c.Text)
		}
	}
	if chunks[0].Span.Start != 100 {
		t.Fatalf("first surviving chunk starts at %d, want 100 (spans still point at the body)", chunks[0].Span.Start)
	}
}

func TestBlankChunksAreDroppedEvenWithoutWhitespaceFolding(t *testing.T) {
	body := strings.Repeat("\t\n", 60) + strings.Repeat("字", 120)

	chunks := WindowChunks(body, mustParams(t, map[string]string{
		ParamStrategy:       "custom",
		ParamChunkSize:      "100",
		ParamOverlapPercent: "0",
	}))

	for _, c := range chunks {
		if strings.TrimSpace(c.Text) == "" {
			t.Fatalf("blank chunk survived: %q", c.Text)
		}
	}
	if len(chunks) == 0 {
		t.Fatal("all chunks were dropped, expected at least one")
	}
}

func TestPreprocessLeavesOrdinaryTextAlone(t *testing.T) {
	text := "这是一段普通的中文教材内容，没有任何需要处理的链接或邮箱。"
	params := ChunkParams{RemoveURL: true, RemoveEmail: true, CollapseWhitespace: true}
	if got := Preprocess(text, params); got != text {
		t.Fatalf("Preprocess changed ordinary text: %q", got)
	}
}

func TestPreprocessRemovesMultipleOccurrences(t *testing.T) {
	text := "a@example.com 与 b@example.org，还有 https://x.example.com 和 http://y.example.com 。"
	params := ChunkParams{RemoveURL: true, RemoveEmail: true, CollapseWhitespace: true}

	got := Preprocess(text, params)
	if strings.Contains(got, "@") || strings.Contains(got, "http") {
		t.Fatalf("Preprocess left something behind: %q", got)
	}
	if !strings.Contains(got, "还有") {
		t.Fatalf("Preprocess removed the surrounding prose: %q", got)
	}
}
