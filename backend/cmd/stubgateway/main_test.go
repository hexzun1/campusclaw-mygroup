package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// cosine works on unit vectors, so the dot product is the cosine.
func cosine(a, b []float64) float64 {
	var dot float64
	for i := range a {
		dot += a[i] * b[i]
	}
	return dot
}

const (
	similarA = "机器学习是人工智能的一个分支，它研究如何让计算机从数据中学习规律并不断改进。"
	similarB = "机器学习是人工智能的重要分支，它研究如何让计算机从数据中学习规律并持续改进。"
	otherA   = "今天下午食堂三楼新出了一道番茄炒蛋，排队的人特别多。"
	otherB   = "图书馆四楼的研讨间需要提前一周在系统里预约，否则很难抢到座位。"
)

func TestEmbedIsDeterministic(t *testing.T) {
	first := Embed(similarA, 256)
	second := Embed(similarA, 256)

	if len(first) != len(second) {
		t.Fatalf("lengths differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("dimension %d differs: %v vs %v", i, first[i], second[i])
		}
	}
}

func TestEmbedDimensionMatchesRequestedDim(t *testing.T) {
	for _, dim := range []int{1, 3, 64, 256, 1536} {
		if got := len(Embed(similarA, dim)); got != dim {
			t.Fatalf("Embed(dim=%d) returned %d values", dim, got)
		}
	}
}

func TestEmbedEmptyTextIsZeroVector(t *testing.T) {
	vec := Embed("", 32)
	for i, v := range vec {
		if v != 0 {
			t.Fatalf("dimension %d = %v, want 0 for empty text", i, v)
		}
	}
}

func TestEmbedIsUnitLength(t *testing.T) {
	for _, v := range Embed(similarA, 256) {
		if v > 1 || v < -1 {
			t.Fatalf("component %v outside [-1,1]", v)
		}
	}
	if got := cosine(Embed(similarA, 256), Embed(similarA, 256)); got < 0.999 {
		t.Fatalf("self-cosine = %v, want ~1", got)
	}
}

func TestEmbedSimilarTextsCosineAboveThreshold(t *testing.T) {
	got := cosine(Embed(similarA, 256), Embed(similarB, 256))
	t.Logf("similar cosine = %v", got)
	if got <= 0.35 {
		t.Fatalf("similar texts scored %v, want > 0.35", got)
	}
}

func TestEmbedUnrelatedTextsCosineBelowThreshold(t *testing.T) {
	got := cosine(Embed(otherA, 256), Embed(otherB, 256))
	t.Logf("unrelated cosine = %v", got)
	if got >= 0.35 {
		t.Fatalf("unrelated texts scored %v, want < 0.35", got)
	}
}

// newTestServer wires the same routes main() serves.
func newTestServer(dim int) *httptest.Server {
	s := &state{dim: dim}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /embeddings", s.handleEmbeddings)
	mux.HandleFunc("POST /chat/completions", s.handleChat)
	mux.HandleFunc("GET /stats", s.handleStats)
	mux.HandleFunc("POST /control", s.handleControl)
	return httptest.NewServer(mux)
}

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func getJSON(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestEmbeddingsEndpointReturnsRequestedDimension(t *testing.T) {
	srv := newTestServer(64)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/embeddings", `{"model":"stub","input":["hello world","second text"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var payload struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Data) != 2 {
		t.Fatalf("got %d embeddings, want 2", len(payload.Data))
	}
	for i, d := range payload.Data {
		if len(d.Embedding) != 64 {
			t.Fatalf("embedding %d has %d dimensions, want 64", i, len(d.Embedding))
		}
	}
}

func TestEmbeddingsEndpointAcceptsPlainStringInput(t *testing.T) {
	srv := newTestServer(8)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/embeddings", `{"model":"stub","input":"one text"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestEmbeddingFailureToggleReturns500(t *testing.T) {
	srv := newTestServer(16)
	defer srv.Close()

	if resp := postJSON(t, srv.URL+"/embeddings", `{"input":"ok"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("status before toggle = %d, want 200", resp.StatusCode)
	}

	postJSON(t, srv.URL+"/control", `{"embed_fail":true}`)

	resp := postJSON(t, srv.URL+"/embeddings", `{"input":"now failing"}`)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status after toggle = %d, want 500", resp.StatusCode)
	}

	postJSON(t, srv.URL+"/control", `{"embed_fail":false}`)
	if resp := postJSON(t, srv.URL+"/embeddings", `{"input":"recovered"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("status after recovery = %d, want 200", resp.StatusCode)
	}
}

func TestChatFailureToggleReturns500(t *testing.T) {
	srv := newTestServer(16)
	defer srv.Close()

	postJSON(t, srv.URL+"/control", `{"chat_fail":true}`)
	resp := postJSON(t, srv.URL+"/chat/completions", `{"messages":[{"role":"user","content":"hi"}]}`)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}

func TestChatEchoesMaterialNumbers(t *testing.T) {
	srv := newTestServer(16)
	defer srv.Close()

	body := `{"model":"stub","messages":[
		{"role":"system","content":"只能依据资料回答。"},
		{"role":"user","content":"资料 [1] 甲\n资料 [2] 乙\n资料 [3] 丙\n问题：是什么？"}]}`
	resp := postJSON(t, srv.URL+"/chat/completions", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var payload struct {
		Choices []struct {
			Message chatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Choices) != 1 {
		t.Fatalf("got %d choices, want 1", len(payload.Choices))
	}
	content := payload.Choices[0].Message.Content
	for _, want := range []string{"[1]", "[2]", "[3]"} {
		if !strings.Contains(content, want) {
			t.Fatalf("content %q does not contain %s", content, want)
		}
	}
	if strings.Contains(content, "[9]") {
		t.Fatalf("content %q unexpectedly contains [9]", content)
	}
}

func TestChatOutOfRangeCitationToggle(t *testing.T) {
	srv := newTestServer(16)
	defer srv.Close()

	postJSON(t, srv.URL+"/control", `{"out_of_range_citation":true}`)
	resp := postJSON(t, srv.URL+"/chat/completions",
		`{"messages":[{"role":"user","content":"资料 [1] 甲\n问题：是什么？"}]}`)

	var payload struct {
		Choices []struct {
			Message chatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload.Choices[0].Message.Content; !strings.Contains(got, "[9]") {
		t.Fatalf("content %q does not contain the toggled [9]", got)
	}
}

func TestChatWithoutMaterialsEmitsNoCitation(t *testing.T) {
	srv := newTestServer(16)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/chat/completions", `{"messages":[{"role":"user","content":"没有资料"}]}`)
	var payload struct {
		Choices []struct {
			Message chatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload.Choices[0].Message.Content; citationPattern.MatchString(got) {
		t.Fatalf("content %q contains a citation but no material was sent", got)
	}
}

func TestStatsReportsCountsAndLastRequests(t *testing.T) {
	srv := newTestServer(16)
	defer srv.Close()

	postJSON(t, srv.URL+"/embeddings", `{"input":"first"}`)
	postJSON(t, srv.URL+"/embeddings", `{"input":"second"}`)
	postJSON(t, srv.URL+"/chat/completions", `{"messages":[{"role":"user","content":"资料 [7] 丙"}]}`)

	resp := getJSON(t, srv.URL+"/stats")
	var stats statsResponse
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if stats.EmbeddingCalls != 2 {
		t.Fatalf("EmbeddingCalls = %d, want 2", stats.EmbeddingCalls)
	}
	if stats.ChatCalls != 1 {
		t.Fatalf("ChatCalls = %d, want 1", stats.ChatCalls)
	}
	if stats.Dim != 16 {
		t.Fatalf("Dim = %d, want 16", stats.Dim)
	}
	if !strings.Contains(string(stats.LastEmbeddingRequest), "second") {
		t.Fatalf("LastEmbeddingRequest = %s, want the most recent body", stats.LastEmbeddingRequest)
	}
	if !strings.Contains(string(stats.LastChatRequest), "[7]") {
		t.Fatalf("LastChatRequest = %s, want the most recent body", stats.LastChatRequest)
	}
}

func TestControlFlipsOneSwitchAtATime(t *testing.T) {
	srv := newTestServer(16)
	defer srv.Close()

	postJSON(t, srv.URL+"/control", `{"embed_fail":true}`)
	postJSON(t, srv.URL+"/control", `{"chat_fail":true}`)

	resp := getJSON(t, srv.URL+"/stats")
	var stats statsResponse
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if !stats.EmbedFail || !stats.ChatFail {
		t.Fatalf("EmbedFail = %v, ChatFail = %v, want both true", stats.EmbedFail, stats.ChatFail)
	}
	if stats.OutOfRangeCitation {
		t.Fatal("OutOfRangeCitation = true, want untouched")
	}
}

func TestEmbeddingsEndpointRejectsMalformedInput(t *testing.T) {
	srv := newTestServer(16)
	defer srv.Close()

	for _, body := range []string{`not json`, `{}`, `{"input":123}`} {
		if resp := postJSON(t, srv.URL+"/embeddings", body); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, want 400", body, resp.StatusCode)
		}
	}
}
