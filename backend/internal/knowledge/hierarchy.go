package knowledge

// HierarchySpans cuts a body on Markdown headings (design.md Decision 5).
//
// Rules:
//   - A heading is a line matching ^#{1,6}\s+\S. A '#' inside a fenced code
//     block (```) is ordinary text.
//   - Everything before the first heading is its own section, so no chunk ever
//     spans a heading boundary.
//   - A section wider than the auto window is re-cut inside its own range with
//     the auto rules; the resulting spans stay absolute offsets into the whole
//     body.
//   - A body without headings is a single section, which the auto window then
//     cuts as usual.
func HierarchySpans(runes []rune) []Span {
	headings := headingOffsets(runes)

	if len(headings) == 0 {
		return splitSection(runes, Span{Start: 0, End: len(runes)})
	}

	var spans []Span
	if headings[0] > 0 {
		spans = append(spans, Span{Start: 0, End: headings[0]})
	}
	for i, start := range headings {
		end := len(runes)
		if i+1 < len(headings) {
			end = headings[i+1]
		}
		spans = append(spans, splitSection(runes, Span{Start: start, End: end})...)
	}
	return spans
}

// splitSection returns the spans covering one heading section: the section
// itself when it fits in a window, otherwise its auto windows shifted back to
// absolute offsets.
func splitSection(runes []rune, section Span) []Span {
	if section.End <= section.Start {
		return nil
	}
	if section.Length() <= AutoSize {
		return []Span{section}
	}

	windows := Windows(runes[section.Start:section.End], AutoSize, AutoOverlap)
	spans := make([]Span, 0, len(windows))
	for _, w := range windows {
		spans = append(spans, Span{Start: section.Start + w.Start, End: section.Start + w.End})
	}
	return spans
}

// headingOffsets returns the character offset of every heading line start, in
// order. Lines inside a fenced code block are never headings.
func headingOffsets(runes []rune) []int {
	var offsets []int
	inFence := false

	lineStart := 0
	for i := 0; i <= len(runes); i++ {
		if i < len(runes) && runes[i] != '\n' {
			continue
		}
		line := runes[lineStart:i]
		switch {
		case isFenceLine(line):
			inFence = !inFence
		case !inFence && isHeadingLine(line):
			offsets = append(offsets, lineStart)
		}
		lineStart = i + 1
	}
	return offsets
}

// isFenceLine reports whether a line opens or closes a ``` code fence. Up to
// three leading spaces are allowed, as in CommonMark.
func isFenceLine(line []rune) bool {
	line = trimTrailingCR(line)

	indent := 0
	for indent < len(line) && line[indent] == ' ' && indent < 3 {
		indent++
	}
	rest := line[indent:]
	return len(rest) >= 3 && rest[0] == '`' && rest[1] == '`' && rest[2] == '`'
}

// isHeadingLine reports whether a line is ^#{1,6}\s+\S.
func isHeadingLine(line []rune) bool {
	line = trimTrailingCR(line)

	hashes := 0
	for hashes < len(line) && line[hashes] == '#' {
		hashes++
	}
	if hashes == 0 || hashes > 6 || hashes >= len(line) {
		return false
	}
	if line[hashes] != ' ' && line[hashes] != '\t' {
		return false
	}
	for _, r := range line[hashes+1:] {
		if r != ' ' && r != '\t' {
			return true
		}
	}
	return false
}

func trimTrailingCR(line []rune) []rune {
	if n := len(line); n > 0 && line[n-1] == '\r' {
		return line[:n-1]
	}
	return line
}
