// Command stubgateway is a deterministic, standard-library-only stand-in for
// the embedding and chat gateways used during development and acceptance runs
// (design.md Decision 11). Acceptance must be repeatable without a real model,
// so this process:
//
//   - POST /embeddings        hashes rune bigrams into EMBEDDING_DIM dimensions
//     and normalizes, so overlapping wording yields a high cosine while
//     unrelated text stays near zero.
//   - POST /chat/completions  echoes the material numbers it received as [n].
//   - GET  /stats             reports per-endpoint call counts and the most
//     recent request bodies, so "the model was not called" is
//     assertable.
//   - POST /control           toggles embedding failure, chat failure and an
//     out-of-range [9] citation.
//
// It is never built into the production image (docker-compose.yml builds only
// ./cmd/server) and carries no credentials.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const maxBodyBytes = 1 << 20

func main() {
	addr := flag.String("addr", envString("STUB_ADDR", ":8090"), "listen address")
	dim := flag.Int("dim", envInt("EMBEDDING_DIM", 256), "embedding dimension")
	flag.Parse()

	if *dim <= 0 {
		log.Fatalf("invalid -dim %d: must be positive", *dim)
	}

	s := &state{dim: *dim}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /embeddings", s.handleEmbeddings)
	mux.HandleFunc("POST /chat/completions", s.handleChat)
	mux.HandleFunc("GET /stats", s.handleStats)
	mux.HandleFunc("POST /control", s.handleControl)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	log.Printf("stubgateway listening on %s (dim=%d)", *addr, *dim)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("stubgateway: %v", err)
	}
}

// Embed maps text to a dim-dimensional unit vector. Every consecutive rune
// bigram (plus a lone trailing rune) is hashed into one bucket with a hashed
// sign, so texts sharing wording point the same way while unrelated texts stay
// near-orthogonal. The mapping is a pure function of (text, dim).
func Embed(text string, dim int) []float64 {
	vec := make([]float64, dim)
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		gram := string(runes[i : i+1])
		if i+1 < len(runes) {
			gram = string(runes[i : i+2])
		}
		h := fnv.New64a()
		h.Write([]byte(gram))
		sum := h.Sum64()
		if (sum>>32)&1 == 1 {
			vec[int(sum%uint64(dim))]--
		} else {
			vec[int(sum%uint64(dim))]++
		}
	}
	normalize(vec)
	return vec
}

func normalize(vec []float64) {
	var sumSquares float64
	for _, v := range vec {
		sumSquares += v * v
	}
	if sumSquares == 0 {
		return
	}
	norm := math.Sqrt(sumSquares)
	for i := range vec {
		vec[i] /= norm
	}
}

type state struct {
	mu sync.Mutex

	dim int

	embeddingCalls int
	chatCalls      int

	lastEmbeddingRequest json.RawMessage
	lastChatRequest      json.RawMessage

	embedFail          bool
	chatFail           bool
	outOfRangeCitation bool
}

type embeddingRequest struct {
	Model string          `json:"model"`
	Input json.RawMessage `json:"input"`
}

func (s *state) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read request body")
		return
	}

	var req embeddingRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "request body is not valid JSON")
		return
	}
	inputs, err := parseEmbeddingInputs(req.Input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.mu.Lock()
	s.embeddingCalls++
	s.lastEmbeddingRequest = append(json.RawMessage(nil), body...)
	fail := s.embedFail
	dim := s.dim
	s.mu.Unlock()

	if fail {
		writeError(w, http.StatusInternalServerError, "stubgateway: embedding failure is toggled on")
		return
	}

	data := make([]map[string]any, 0, len(inputs))
	for i, in := range inputs {
		data = append(data, map[string]any{
			"object":    "embedding",
			"index":     i,
			"embedding": Embed(in, dim),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
		"model":  req.Model,
		"usage":  map[string]int{"prompt_tokens": 0, "total_tokens": 0},
	})
}

// parseEmbeddingInputs accepts both the string and the array form of the
// OpenAI-compatible "input" field.
func parseEmbeddingInputs(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing input")
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, nil
	}
	return nil, fmt.Errorf("input must be a string or an array of strings")
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

func (s *state) handleChat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read request body")
		return
	}

	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "request body is not valid JSON")
		return
	}

	s.mu.Lock()
	s.chatCalls++
	s.lastChatRequest = append(json.RawMessage(nil), body...)
	fail := s.chatFail
	outOfRange := s.outOfRangeCitation
	s.mu.Unlock()

	if fail {
		writeError(w, http.StatusInternalServerError, "stubgateway: chat failure is toggled on")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":     "stub-chat-completion",
		"object": "chat.completion",
		"model":  req.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       chatMessage{Role: "assistant", Content: draftAnswer(&req, outOfRange)},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
	})
}

var citationPattern = regexp.MustCompile(`\[(\d+)\]`)

// draftAnswer echoes the material numbers found in the prompt as [n] markers,
// which is exactly what citations are numbered by (design.md Decision 9). With
// the out-of-range switch on it also emits [9], so the server-side removal of
// markers beyond len(citations) can be asserted.
func draftAnswer(req *chatRequest, outOfRange bool) string {
	numbers := materialNumbers(req)
	if len(numbers) == 0 {
		return "stubgateway 没有收到任何资料。"
	}

	var b strings.Builder
	b.WriteString("stubgateway 依据收到的资料作答：")
	for _, n := range numbers {
		fmt.Fprintf(&b, " [%d]", n)
	}
	if outOfRange {
		b.WriteString(" [9]")
	}
	return b.String()
}

// materialNumbers returns the distinct [n] numbers in the prompt, ascending.
func materialNumbers(req *chatRequest) []int {
	seen := map[int]bool{}
	var numbers []int
	for _, m := range req.Messages {
		for _, match := range citationPattern.FindAllStringSubmatch(m.Content, -1) {
			n, err := strconv.Atoi(match[1])
			if err != nil || seen[n] {
				continue
			}
			seen[n] = true
			numbers = append(numbers, n)
		}
	}
	sort.Ints(numbers)
	return numbers
}

type statsResponse struct {
	Dim                  int             `json:"dim"`
	EmbeddingCalls       int             `json:"embedding_calls"`
	ChatCalls            int             `json:"chat_calls"`
	LastEmbeddingRequest json.RawMessage `json:"last_embedding_request"`
	LastChatRequest      json.RawMessage `json:"last_chat_request"`
	EmbedFail            bool            `json:"embed_fail"`
	ChatFail             bool            `json:"chat_fail"`
	OutOfRangeCitation   bool            `json:"out_of_range_citation"`
}

func (s *state) handleStats(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	resp := statsResponse{
		Dim:                  s.dim,
		EmbeddingCalls:       s.embeddingCalls,
		ChatCalls:            s.chatCalls,
		LastEmbeddingRequest: s.lastEmbeddingRequest,
		LastChatRequest:      s.lastChatRequest,
		EmbedFail:            s.embedFail,
		ChatFail:             s.chatFail,
		OutOfRangeCitation:   s.outOfRangeCitation,
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, resp)
}

// controlRequest uses pointers so a caller can flip one switch without
// resetting the others.
type controlRequest struct {
	EmbedFail          *bool `json:"embed_fail"`
	ChatFail           *bool `json:"chat_fail"`
	OutOfRangeCitation *bool `json:"out_of_range_citation"`
}

func (s *state) handleControl(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read request body")
		return
	}

	var req controlRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "request body is not valid JSON")
		return
	}

	s.mu.Lock()
	if req.EmbedFail != nil {
		s.embedFail = *req.EmbedFail
	}
	if req.ChatFail != nil {
		s.chatFail = *req.ChatFail
	}
	if req.OutOfRangeCitation != nil {
		s.outOfRangeCitation = *req.OutOfRangeCitation
	}
	resp := map[string]bool{
		"embed_fail":            s.embedFail,
		"chat_fail":             s.chatFail,
		"out_of_range_citation": s.outOfRangeCitation,
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("stubgateway: write response: %v", err)
	}
}

// writeError never includes upstream addresses or keys: this stub has none,
// but the shape matches the server's own 503 bodies.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func envString(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
