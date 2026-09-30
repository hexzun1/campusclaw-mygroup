// Package knowledge turns a knowledge entry's body text into chunks that stay
// traceable: every chunk is a left-closed, right-open range of Unicode
// characters in the untouched body, plus the text derived from that range
// (design.md Decision 5).
//
// All offsets count runes rather than bytes, so Chinese text and emoji are
// never split mid-character.
package knowledge

// Auto strategy values: 800 characters per chunk with 80 characters of overlap
// (spec: 切分策略).
const (
	AutoSize    = 800
	AutoOverlap = 80
)

// Span is a left-closed, right-open character range [Start, End) into the body.
type Span struct {
	Start int
	End   int
}

// Length is the number of characters the span covers.
func (s Span) Length() int { return s.End - s.Start }

// Windows splits runes into windows of size characters that share overlap
// characters with their neighbours.
//
// The walk is the one described in design.md Decision 5: start at 0, emit
// [start, min(start+size, n)), stop once the window reaches the end, otherwise
// advance by size-overlap. A 2000-character body with the auto values therefore
// yields [0,800), [720,1520), [1440,2000).
//
// Callers validate that overlap < size; a non-positive step is clamped to 1 so
// this function can never loop forever.
func Windows(runes []rune, size, overlap int) []Span {
	if len(runes) == 0 || size <= 0 {
		return nil
	}
	step := size - overlap
	if step < 1 {
		step = 1
	}

	var spans []Span
	for start := 0; start < len(runes); start += step {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		spans = append(spans, Span{Start: start, End: end})
		if end == len(runes) {
			break
		}
	}
	return spans
}

// AutoWindows splits runes with the default auto strategy.
func AutoWindows(runes []rune) []Span {
	return Windows(runes, AutoSize, AutoOverlap)
}

// OverlapFor converts a percentage of the chunk size into a character count,
// rounding down. The custom strategy allows 0-50 percent (spec: 切分策略).
func OverlapFor(size, percent int) int {
	if size <= 0 || percent <= 0 {
		return 0
	}
	return size * percent / 100
}

// SpanText returns the characters a span covers. It is the caller's job to
// ensure the span is inside runes.
func SpanText(runes []rune, s Span) string {
	if s.Start < 0 || s.End > len(runes) || s.Start >= s.End {
		return ""
	}
	return string(runes[s.Start:s.End])
}
