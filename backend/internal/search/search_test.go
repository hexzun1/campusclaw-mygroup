package search

import (
	"strings"
	"testing"
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
