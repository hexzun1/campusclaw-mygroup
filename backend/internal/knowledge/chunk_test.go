package knowledge

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func repeatRunes(pattern string, count int) []rune {
	return []rune(strings.Repeat(pattern, count))
}

func TestAutoWindowsOnTwoThousandCharacters(t *testing.T) {
	runes := repeatRunes("字", 2000)

	spans := Windows(runes, AutoSize, AutoOverlap)

	want := []Span{{0, 800}, {720, 1520}, {1440, 2000}}
	if len(spans) != len(want) {
		t.Fatalf("got %d spans (%v), want %d", len(spans), spans, len(want))
	}
	for i := range want {
		if spans[i] != want[i] {
			t.Fatalf("span %d = %v, want %v", i, spans[i], want[i])
		}
	}
}

func TestAutoWindowsFromAutoHelperMatchesExplicitValues(t *testing.T) {
	runes := repeatRunes("ab", 500)

	auto := AutoWindows(runes)
	explicit := Windows(runes, AutoSize, AutoOverlap)
	if len(auto) != len(explicit) {
		t.Fatalf("AutoWindows returned %d spans, explicit %d", len(auto), len(explicit))
	}
	for i := range auto {
		if auto[i] != explicit[i] {
			t.Fatalf("span %d: AutoWindows %v, explicit %v", i, auto[i], explicit[i])
		}
	}
}

// TestCustomWindowsStepAndLength covers "片长 100、重叠 10% 的 300 字得到起点
// 间隔 90 且每片 ≤ 100".
func TestCustomWindowsStepAndLength(t *testing.T) {
	runes := repeatRunes("字", 300)

	spans := Windows(runes, 100, OverlapFor(100, 10))

	if len(spans) < 2 {
		t.Fatalf("got %d spans, want several", len(spans))
	}
	if spans[0].Start != 0 {
		t.Fatalf("first span starts at %d, want 0", spans[0].Start)
	}
	for i, s := range spans {
		if s.Length() > 100 {
			t.Fatalf("span %d has length %d, want <= 100", i, s.Length())
		}
		if i > 0 {
			if step := s.Start - spans[i-1].Start; step != 90 {
				t.Fatalf("start interval between span %d and %d is %d, want 90", i-1, i, step)
			}
		}
	}
	last := spans[len(spans)-1]
	if last.End != 300 {
		t.Fatalf("last span ends at %d, want 300", last.End)
	}
}

func TestWindowsCoverTheWholeTextWithoutGaps(t *testing.T) {
	runes := repeatRunes("好", 250)

	spans := Windows(runes, 100, 20)

	if spans[0].Start != 0 {
		t.Fatalf("first span starts at %d, want 0", spans[0].Start)
	}
	if spans[len(spans)-1].End != len(runes) {
		t.Fatalf("last span ends at %d, want %d", spans[len(spans)-1].End, len(runes))
	}
	for i := 1; i < len(spans); i++ {
		if spans[i].Start > spans[i-1].End {
			t.Fatalf("gap between span %d (%v) and %d (%v)", i-1, spans[i-1], i, spans[i])
		}
	}
}

func TestWindowsShorterThanWindowYieldsOneSpan(t *testing.T) {
	runes := repeatRunes("短", 100)

	spans := Windows(runes, AutoSize, AutoOverlap)

	if len(spans) != 1 {
		t.Fatalf("got %d spans (%v), want 1", len(spans), spans)
	}
	if spans[0] != (Span{0, 100}) {
		t.Fatalf("span = %v, want {0 100}", spans[0])
	}
}

func TestWindowsOnEmptyTextYieldsNothing(t *testing.T) {
	if spans := Windows(nil, AutoSize, AutoOverlap); len(spans) != 0 {
		t.Fatalf("got %v, want no spans", spans)
	}
	if spans := Windows([]rune{}, AutoSize, AutoOverlap); len(spans) != 0 {
		t.Fatalf("got %v, want no spans", spans)
	}
}

func TestWindowsWithZeroOverlapAreDisjoint(t *testing.T) {
	runes := repeatRunes("x", 250)

	spans := Windows(runes, 100, 0)

	want := []Span{{0, 100}, {100, 200}, {200, 250}}
	if len(spans) != len(want) {
		t.Fatalf("got %v, want %v", spans, want)
	}
	for i := range want {
		if spans[i] != want[i] {
			t.Fatalf("span %d = %v, want %v", i, spans[i], want[i])
		}
	}
}

// TestOffsetsCountCharactersNotBytes is the reason the whole package works on
// []rune: a byte-based walk would produce different spans for this text.
func TestOffsetsCountCharactersNotBytes(t *testing.T) {
	text := "中文emoji😀测试"
	runes := []rune(text)

	if utf8.RuneCountInString(text) == len(text) {
		t.Fatal("fixture is pure ASCII, it cannot distinguish bytes from characters")
	}
	if len(runes) != 10 {
		t.Fatalf("fixture has %d runes, want 10", len(runes))
	}

	spans := Windows(runes, 4, 0)

	want := []Span{{0, 4}, {4, 8}, {8, 10}}
	if len(spans) != len(want) {
		t.Fatalf("got %v, want %v", spans, want)
	}
	for i := range want {
		if spans[i] != want[i] {
			t.Fatalf("span %d = %v, want %v", i, spans[i], want[i])
		}
	}

	// Reassembling the spans must reproduce the original text exactly, which
	// only holds if the offsets are character offsets into []rune.
	var rebuilt strings.Builder
	for _, s := range spans {
		rebuilt.WriteString(SpanText(runes, s))
	}
	if rebuilt.String() != text {
		t.Fatalf("rebuilt %q, want %q", rebuilt.String(), text)
	}
}

func TestOverlapFor(t *testing.T) {
	for _, tc := range []struct {
		size, percent, want int
	}{
		{100, 10, 10},
		{100, 0, 0},
		{100, 50, 50},
		{200, 10, 20},
		{101, 10, 10},
		{0, 10, 0},
		{100, -1, 0},
	} {
		if got := OverlapFor(tc.size, tc.percent); got != tc.want {
			t.Errorf("OverlapFor(%d, %d) = %d, want %d", tc.size, tc.percent, got, tc.want)
		}
	}
}

// TestWindowsTerminatesWithDegenerateOverlap guards the loop against a caller
// that bypasses parameter validation.
func TestWindowsTerminatesWithDegenerateOverlap(t *testing.T) {
	runes := repeatRunes("x", 10)

	spans := Windows(runes, 5, 5)
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}
	if spans[len(spans)-1].End != len(runes) {
		t.Fatalf("last span %v does not reach the end of the text", spans[len(spans)-1])
	}
}

func TestSpanTextRejectsOutOfRange(t *testing.T) {
	runes := []rune("abcdef")
	if got := SpanText(runes, Span{0, 3}); got != "abc" {
		t.Fatalf("SpanText = %q, want abc", got)
	}
	for _, bad := range []Span{{-1, 2}, {0, 99}, {4, 4}, {5, 2}} {
		if got := SpanText(runes, bad); got != "" {
			t.Fatalf("SpanText(%v) = %q, want empty", bad, got)
		}
	}
}
