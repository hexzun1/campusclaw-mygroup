package knowledge

// ChunkBody cuts a knowledge entry body into chunks with the caller's strategy
// and preprocessing. It is the single entry point the upload, reindex and
// compensation paths use, so all of them chunk identically.
func ChunkBody(body string, params ChunkParams) []Chunk {
	runes := []rune(body)

	var spans []Span
	if params.Strategy == StrategyHierarchy {
		spans = HierarchySpans(runes)
	} else {
		size, overlap := params.Window()
		spans = Windows(runes, size, overlap)
	}
	return ChunksFromSpans(runes, spans, params)
}
