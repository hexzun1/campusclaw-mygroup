package tutor

import (
	"strconv"
	"strings"
	"unicode"
)

// bannedAnswer is the phrase an assistant answer must never be: a turn whose
// complete output equals it is a failed generation, not a reply
// (spec: 会话的作答口径).
const bannedAnswer = "资料中未找到相关内容"

// OutputFilter turns raw gateway fragments into the text a browser may see:
// a [n] marker survives only when n points at one of this turn's citations,
// and text that may still become the banned answer is withheld until it
// cannot. Every Feed output is a delta; their concatenation is exactly what
// gets persisted, so "delta 拼接 = 落库正文" holds by construction.
type OutputFilter struct {
	citations int

	bracket strings.Builder // "[" plus the digits collected so far
	total   strings.Builder // every filtered character, for the final check
	held    strings.Builder // withheld text that may still become bannedAnswer
	holding bool
}

// NewOutputFilter builds the filter of one turn with n citations.
func NewOutputFilter(citations int) *OutputFilter {
	return &OutputFilter{citations: citations, holding: true}
}

// Feed consumes one raw fragment and returns the text to push now. It is
// empty while the fragment ends inside an unfinished [n] marker or while the
// output so far could still become the banned answer.
func (f *OutputFilter) Feed(piece string) string {
	return f.emit(f.strain(piece))
}

// strain is the citation pass. A marker is emitted only when it closes and
// points at one of the turn's citations; anything else — an unfinished
// marker, a marker split across fragments, a non-numeric bracket or a
// number beyond the citation count — is dropped or returned as ordinary
// text.
func (f *OutputFilter) strain(piece string) string {
	var out strings.Builder
	for _, r := range piece {
		if f.bracket.Len() > 0 {
			if r >= '0' && r <= '9' {
				f.bracket.WriteRune(r)
				continue
			}
			if r == ']' && f.bracket.Len() > 1 {
				n, err := strconv.Atoi(f.bracket.String()[1:])
				if err == nil && n >= 1 && n <= f.citations {
					out.WriteString(f.bracket.String())
					out.WriteRune(']')
				}
				f.bracket.Reset()
				continue
			}
			out.WriteString(f.bracket.String())
			f.bracket.Reset()
			// fall through: r is ordinary text (or starts a new marker)
		}
		if r == '[' {
			f.bracket.WriteRune(r)
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// emit is the banned-answer guard: while everything seen so far — without
// leading whitespace — is still a prefix of bannedAnswer, the text is held
// back; the first divergence releases the whole withheld run verbatim.
func (f *OutputFilter) emit(filtered string) string {
	f.total.WriteString(filtered)
	if !f.holding {
		return filtered
	}
	f.held.WriteString(filtered)
	if strings.HasPrefix(bannedAnswer, strings.TrimLeftFunc(f.held.String(), unicode.IsSpace)) {
		return ""
	}
	f.holding = false
	out := f.held.String()
	f.held.Reset()
	return out
}

// Finish flushes the end of the stream. failed reports that the complete
// output is exactly the banned answer, in which case nothing was ever
// released and nothing may be pushed or persisted. Otherwise the returned
// text is the final piece — possibly an unfinished marker, which is ordinary
// text by then. Call it once, after the last Feed.
func (f *OutputFilter) Finish() (text string, failed bool) {
	var out string
	if f.bracket.Len() > 0 {
		out = f.emit(f.bracket.String())
		f.bracket.Reset()
	}
	if strings.TrimSpace(f.total.String()) == bannedAnswer {
		return "", true
	}
	if f.holding {
		f.holding = false
		out += f.held.String()
		f.held.Reset()
	}
	return out, false
}
