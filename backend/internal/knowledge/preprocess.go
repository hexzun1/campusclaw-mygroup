package knowledge

import (
	"regexp"
	"strings"
)

// Chunk is one produced slice: the range it covers in the untouched body, plus
// the text actually stored for retrieval.
//
// Text is derived from the body's characters in [Span.Start, Span.End) but may
// differ from them, because preprocessing only ever rewrites the chunk text
// (design.md Decision 5). Span always points at the original body, so every hit
// stays traceable.
type Chunk struct {
	Index int
	Span  Span
	Text  string
}

// Preprocessing patterns. They are deliberately conservative: they cover the
// common forms, not every legal URL or RFC 5322 address.
var (
	urlPattern   = regexp.MustCompile(`https?://[^\s]+`)
	emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	spacePattern = regexp.MustCompile(`\s+`)
)

// Preprocess rewrites one chunk's text: URLs first, then e-mail addresses, then
// whitespace folding (design.md Decision 5). Each step is off unless the caller
// asked for it, and the body text is never touched.
func Preprocess(text string, params ChunkParams) string {
	if params.RemoveURL {
		text = urlPattern.ReplaceAllString(text, "")
	}
	if params.RemoveEmail {
		text = emailPattern.ReplaceAllString(text, "")
	}
	if params.CollapseWhitespace {
		// Any run of whitespace becomes a single space, and leading/trailing
		// whitespace is dropped.
		text = strings.TrimSpace(spacePattern.ReplaceAllString(text, " "))
	}
	return text
}

// ChunksFromSpans builds the chunk list for a body: each span's raw characters
// are preprocessed, chunks that end up blank are dropped, and the survivors are
// numbered from 0 without gaps.
//
// A span whose text is only whitespace counts as blank: it would be useless for
// retrieval, and dropping it keeps the stored chunk list meaningful. Dropping
// never touches the body or the spans of the chunks that remain.
func ChunksFromSpans(runes []rune, spans []Span, params ChunkParams) []Chunk {
	chunks := make([]Chunk, 0, len(spans))
	for _, span := range spans {
		text := Preprocess(SpanText(runes, span), params)
		if strings.TrimSpace(text) == "" {
			continue
		}
		chunks = append(chunks, Chunk{Index: len(chunks), Span: span, Text: text})
	}
	return chunks
}

// WindowChunks cuts a body with the fixed windows of the auto or custom
// strategy, applying the caller's preprocessing.
func WindowChunks(body string, params ChunkParams) []Chunk {
	runes := []rune(body)
	size, overlap := params.Window()
	return ChunksFromSpans(runes, Windows(runes, size, overlap), params)
}
