package search

import (
	"strings"
	"testing"

	"campusclaw/backend/internal/db"
)

func TestParseRequestDefaults(t *testing.T) {
	req, err := ParseRequest([]byte(`{"query":"教学楼"}`))
	if err != nil {
		t.Fatalf("ParseRequest: %v", err)
	}
	if req.Query != "教学楼" {
		t.Errorf("Query = %q, want %q", req.Query, "教学楼")
	}
	if req.Mode != ModeHybrid {
		t.Errorf("Mode = %q, want %q", req.Mode, ModeHybrid)
	}
	if req.TopK != DefaultTopK {
		t.Errorf("TopK = %d, want %d", req.TopK, DefaultTopK)
	}
}

func TestParseRequestTrimsQuery(t *testing.T) {
	req, err := ParseRequest([]byte(`{"query":"  open day \n"}`))
	if err != nil {
		t.Fatalf("ParseRequest: %v", err)
	}
	if req.Query != "open day" {
		t.Errorf("Query = %q, want the trimmed %q", req.Query, "open day")
	}
}

func TestParseRequestIgnoresAClassField(t *testing.T) {
	// A class_id in the body must not fail validation nor reach the request:
	// the class comes from the session alone.
	req, err := ParseRequest([]byte(`{"query":"教学楼","class_id":2,"role":"teacher"}`))
	if err != nil {
		t.Fatalf("ParseRequest: %v", err)
	}
	if req.Mode != ModeHybrid || req.TopK != DefaultTopK {
		t.Errorf("unknown fields changed the parsed request: %+v", req)
	}
}

func TestParseRequestAcceptsBoundaries(t *testing.T) {
	longest := strings.Repeat("字", MaxQueryChars)
	for _, body := range []string{
		`{"query":"a","mode":"keyword","top_k":1}`,
		`{"query":"a","mode":"vector","top_k":20}`,
		`{"query":"a","mode":"hybrid"}`,
		`{"query":"a","top_k":null}`,
		`{"query":"` + longest + `"}`,
	} {
		if _, err := ParseRequest([]byte(body)); err != nil {
			t.Errorf("ParseRequest(%s) = %v, want nil", body, err)
		}
	}
}

func TestParseRequestRejects(t *testing.T) {
	tooLong := strings.Repeat("字", MaxQueryChars+1)
	tests := []struct {
		name string
		body string
	}{
		{"not JSON", `{`},
		{"empty body", ``},
		{"missing query", `{"mode":"keyword"}`},
		{"empty query", `{"query":""}`},
		{"blank query", `{"query":" \t\n "}`},
		{"query too long", `{"query":"` + tooLong + `"}`},
		{"unknown mode", `{"query":"a","mode":"x"}`},
		{"mode case mismatch", `{"query":"a","mode":"Hybrid"}`},
		{"top_k zero", `{"query":"a","top_k":0}`},
		{"top_k too large", `{"query":"a","top_k":21}`},
		{"top_k negative", `{"query":"a","top_k":-1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseRequest([]byte(tt.body)); err == nil {
				t.Errorf("ParseRequest(%s) = nil error, want a rejection", tt.body)
			}
		})
	}
}

func ids(hits []Hit) []int64 {
	out := make([]int64, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.ChunkID)
	}
	return out
}

func chunkHit(id int64) db.ChunkHit {
	return db.ChunkHit{Chunk: db.Chunk{ID: id}}
}

func TestFuseRRF(t *testing.T) {
	// In one branch, both chunks rank (1, 2); the other branch returns chunk 11
	// at rank 2 and pushes its own first chunk above it.
	keyword := []db.ChunkHit{chunkHit(11), chunkHit(12)}
	vector := []db.ChunkHit{chunkHit(13), chunkHit(11)}

	fused := fuseRRF(keyword, vector)

	// 11: 1/61 + 1/62, 13: 1/61, 12: 1/62.
	wantOrder := []int64{11, 13, 12}
	if got := ids(toHits(fused, len(fused))); !equalIDs(got, wantOrder) {
		t.Fatalf("fused order = %v, want %v", got, wantOrder)
	}

	wantScores := map[int64]float64{
		11: 1.0/61 + 1.0/62,
		13: 1.0 / 61,
		12: 1.0 / 62,
	}
	for _, hit := range fused {
		if diff := hit.Score - wantScores[hit.ID]; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("chunk %d score = %v, want %v", hit.ID, hit.Score, wantScores[hit.ID])
		}
	}
}

func TestFuseRRFTiesBreakByID(t *testing.T) {
	fused := fuseRRF([]db.ChunkHit{chunkHit(22)}, []db.ChunkHit{chunkHit(21)})
	if got := ids(toHits(fused, len(fused))); !equalIDs(got, []int64{21, 22}) {
		t.Errorf("tied chunks ordered %v, want ID ascending [21 22]", got)
	}
}

func TestFuseRRFWithOneEmptyBranch(t *testing.T) {
	keyword := []db.ChunkHit{chunkHit(11), chunkHit(12)}
	fused := fuseRRF(keyword, nil)
	if got := ids(toHits(fused, len(fused))); !equalIDs(got, []int64{11, 12}) {
		t.Fatalf("order = %v, want the keyword ranking [11 12]", got)
	}
	if diff := fused[0].Score - 1.0/61; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("first score = %v, want 1/61", fused[0].Score)
	}
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestToHitsCapsAndProjects(t *testing.T) {
	rows := []db.ChunkHit{
		{Chunk: db.Chunk{ID: 7, MaterialID: 3, ChunkIndex: 1, CharStart: 10, CharEnd: 20, ChunkText: "第一段正文"}, MaterialTitle: "材料一", Score: 1.5},
		{Chunk: db.Chunk{ID: 8, MaterialID: 3, ChunkIndex: 2, CharStart: 20, CharEnd: 30, ChunkText: "第二段正文"}, MaterialTitle: "材料一", Score: 0.5},
	}

	hits := toHits(rows, 1)
	if len(hits) != 1 {
		t.Fatalf("toHits returned %d hits, want 1", len(hits))
	}
	want := Hit{MaterialID: 3, MaterialTitle: "材料一", ChunkID: 7, ChunkIndex: 1, CharStart: 10, CharEnd: 20, Excerpt: "第一段正文", Score: 1.5}
	if hits[0] != want {
		t.Errorf("hits[0] = %+v, want %+v", hits[0], want)
	}

	if empty := toHits(nil, 5); empty == nil || len(empty) != 0 {
		t.Errorf("toHits(nil) = %v, want a non-nil empty slice", empty)
	}
}

func TestExcerpt(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"short text is unchanged", "教学楼开放日", "教学楼开放日"},
		{"exact length is unchanged", strings.Repeat("字", ExcerptChars), strings.Repeat("字", ExcerptChars)},
		{"longer text is cut and marked", strings.Repeat("字", ExcerptChars+1), strings.Repeat("字", ExcerptChars) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Excerpt(tt.text); got != tt.want {
				t.Errorf("Excerpt returned %d chars, want %d chars", len([]rune(got)), len([]rune(tt.want)))
			}
		})
	}
}
