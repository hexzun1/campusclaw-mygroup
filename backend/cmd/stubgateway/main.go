// Command stubgateway is a deterministic, standard-library-only stand-in for
// the embedding and chat gateways used during development and acceptance runs
// (design.md Decision 11). Acceptance must be repeatable without a real model,
// so this process:
//
//   - POST /embeddings        hashes rune bigrams into EMBEDDING_DIM dimensions
//     and normalizes, so overlapping wording yields a high cosine while
//     unrelated text stays near zero.
//   - POST /chat/completions  echoes the material numbers it received as [n];
//     with stream:true it sends the same text back as SSE pieces.
//   - GET  /stats             reports per-endpoint call counts and the most
//     recent request bodies, so "the model was not called" is
//     assertable.
//   - POST /control           toggles embedding failure, chat failure, an
//     out-of-range [9] citation, a pre-stream failure, a mid-stream abort and
//     an exact answer text.
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
	"time"
)

const maxBodyBytes = 1 << 20

// Stream piece size and pace: small enough that a testable answer arrives as
// several SSE frames, slow enough that "arrived piecewise" is observable.
const (
	streamPieceRunes = 3
	streamPiecePause = 20 * time.Millisecond
)

func main() {
	addr := flag.String("addr", envString("STUB_ADDR", ":8090"), "listen address")
	dim := flag.Int("dim", envInt("EMBEDDING_DIM", 256), "embedding dimension")
	flag.Parse()

	if *dim <= 0 {
		log.Fatalf("invalid -dim %d: must be positive", *dim)
	}

	// onceToolCalls is on by default: a forced list belongs to one tool stage,
	// not to every later request (design.md Decision 13).
	s := &state{dim: *dim, abortAfterDeltas: -1, onceToolCalls: true}

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

	// Streaming switches (design.md Decision 8): failBeforeFirstDelta answers
	// the stream request with a plain 500 before any SSE frame;
	// abortAfterDeltas >= 0 sends that many pieces and then aborts the
	// connection; chatText, when non-empty, replaces the echoed answer text.
	failBeforeFirstDelta bool
	abortAfterDeltas     int
	chatText             string

	// forcedToolCalls, when non-nil, replaces the deterministic tool call:
	// an empty list means "answer without calling any tool" (design.md
	// Decision 12). The pointed-to slice is replaced wholesale on control, and
	// onceToolCalls decides whether the next tool-stage request consumes it
	// (Decision 13).
	forcedToolCalls *[]forcedToolCall
	onceToolCalls   bool
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

type toolDefinition struct {
	Type     string `json:"type"`
	Function struct {
		Name       string `json:"name"`
		Parameters struct {
			Required   []string `json:"required"`
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
		} `json:"parameters"`
	} `json:"function"`
}

type chatRequest struct {
	Model      string           `json:"model"`
	Messages   []chatMessage    `json:"messages"`
	Stream     bool             `json:"stream"`
	Tools      []toolDefinition `json:"tools"`
	ToolChoice json.RawMessage  `json:"tool_choice"`
}

// forcedToolCall is one entry of the /control "指定工具调用" switch. Arguments
// is the raw string the model would have produced, so invalid JSON can be
// exercised on purpose.
type forcedToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type stubToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
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
	failBeforeFirst := s.failBeforeFirstDelta
	abortAfter := s.abortAfterDeltas
	chatText := s.chatText
	forcedSet := s.forcedToolCalls != nil
	var forced []forcedToolCall
	if forcedSet {
		forced = append([]forcedToolCall(nil), (*s.forcedToolCalls)...)
		// A non-empty forced list drives one tool-stage request only: the turn's
		// second non-streamed request carries tool messages and must answer with
		// text, not repeat the same calls (design.md Decision 13). An empty list
		// stays until reset, because "this stub never calls tools" is a session
		// switch other checks rely on.
		if s.onceToolCalls && len(forced) > 0 && len(req.Tools) > 0 {
			s.forcedToolCalls = nil
		}
	}
	s.mu.Unlock()

	if fail {
		writeError(w, http.StatusInternalServerError, "stubgateway: chat failure is toggled on")
		return
	}

	if req.Stream {
		s.streamChat(w, &req, outOfRange, failBeforeFirst, abortAfter, chatText)
		return
	}

	message := map[string]any{"role": "assistant", "content": answerText(&req, outOfRange, chatText)}
	finish := "stop"
	if calls, ok := stubToolCalls(&req, forced, forcedSet); ok {
		message["content"] = ""
		message["tool_calls"] = calls
		finish = "tool_calls"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":     "stub-chat-completion",
		"object": "chat.completion",
		"model":  req.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       message,
			"finish_reason": finish,
		}},
		"usage": map[string]int{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
	})
}

// answerText is the answer the stub produces: the exact text when the control
// switch sets one, the material echo otherwise.
func answerText(req *chatRequest, outOfRange bool, chatText string) string {
	if chatText != "" {
		return chatText
	}
	return draftAnswer(req, outOfRange)
}

// stubToolCalls decides whether a non-streamed request with tools produces a
// tool call (design.md Decision 12). An explicit /control list wins, including
// an empty one ("answer without tools"); otherwise the first turn calls the
// first declared tool and any turn already carrying a tool message answers
// with text.
func stubToolCalls(req *chatRequest, forced []forcedToolCall, forcedSet bool) ([]stubToolCall, bool) {
	if len(req.Tools) == 0 {
		return nil, false
	}
	if forcedSet {
		if len(forced) == 0 {
			return nil, false
		}
		calls := make([]stubToolCall, 0, len(forced))
		for i, f := range forced {
			calls = append(calls, newStubToolCall(i, f.Name, f.Arguments))
		}
		return calls, true
	}

	for _, m := range req.Messages {
		if m.Role == "tool" {
			return nil, false
		}
	}

	first := req.Tools[0].Function
	args := map[string]any{}
	question := lastQuestion(req.Messages)
	for _, name := range first.Parameters.Required {
		prop := first.Parameters.Properties[name]
		switch prop.Type {
		case "number", "integer":
			args[name] = 1
		case "boolean":
			args[name] = true
		default:
			args[name] = question
		}
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		encoded = []byte(`{}`)
	}
	return []stubToolCall{newStubToolCall(0, first.Name, string(encoded))}, true
}

func newStubToolCall(index int, name, arguments string) stubToolCall {
	call := stubToolCall{ID: fmt.Sprintf("call_stub_%d", index+1), Type: "function"}
	call.Function.Name = name
	call.Function.Arguments = arguments
	return call
}

// lastQuestion returns the text after the last 「学生提问：」 marker, which is
// where the server places the student's question (tutor-chat 消息组装).
func lastQuestion(messages []chatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		if idx := strings.LastIndex(messages[i].Content, "学生提问："); idx >= 0 {
			return strings.TrimSpace(messages[i].Content[idx+len("学生提问："):])
		}
	}
	return ""
}

// streamChat answers with the same text as SSE frames of streamPieceRunes
// runes each. The header is written and flushed before the first piece, so a
// slow or aborted stream is distinguishable from a pre-stream failure; an
// abort panics with http.ErrAbortHandler, which drops the connection without a
// response trailer — exactly the "upstream died mid-stream" shape the server
// must handle.
func (s *state) streamChat(w http.ResponseWriter, req *chatRequest, outOfRange, failBeforeFirst bool, abortAfter int, chatText string) {
	if failBeforeFirst {
		writeError(w, http.StatusInternalServerError, "stubgateway: chat failure is toggled on")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stubgateway: streaming is unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	pieces := splitRunes(answerText(req, outOfRange, chatText), streamPieceRunes)
	for i, piece := range pieces {
		if abortAfter >= 0 && i >= abortAfter {
			panic(http.ErrAbortHandler)
		}
		if err := writeChatChunk(w, req.Model, piece); err != nil {
			return
		}
		flusher.Flush()
		time.Sleep(streamPiecePause)
	}

	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func writeChatChunk(w io.Writer, model, piece string) error {
	chunk := map[string]any{
		"id":     "stub-chat-completion",
		"object": "chat.completion.chunk",
		"model":  model,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         map[string]string{"content": piece},
			"finish_reason": nil,
		}},
	}
	encoded, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", encoded)
	return err
}

// splitRunes cuts text into consecutive pieces of at most n runes.
func splitRunes(text string, n int) []string {
	runes := []rune(text)
	var pieces []string
	for start := 0; start < len(runes); start += n {
		end := start + n
		if end > len(runes) {
			end = len(runes)
		}
		pieces = append(pieces, string(runes[start:end]))
	}
	return pieces
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
	Dim                  int               `json:"dim"`
	EmbeddingCalls       int               `json:"embedding_calls"`
	ChatCalls            int               `json:"chat_calls"`
	LastEmbeddingRequest json.RawMessage   `json:"last_embedding_request"`
	LastChatRequest      json.RawMessage   `json:"last_chat_request"`
	EmbedFail            bool              `json:"embed_fail"`
	ChatFail             bool              `json:"chat_fail"`
	OutOfRangeCitation   bool              `json:"out_of_range_citation"`
	FailBeforeFirstDelta bool              `json:"fail_before_first_delta"`
	AbortAfterDeltas     int               `json:"abort_after_deltas"`
	ChatText             string            `json:"chat_text"`
	ToolCalls            *[]forcedToolCall `json:"tool_calls"`
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
		FailBeforeFirstDelta: s.failBeforeFirstDelta,
		AbortAfterDeltas:     s.abortAfterDeltas,
		ChatText:             s.chatText,
		ToolCalls:            s.forcedToolCalls,
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, resp)
}

// controlRequest uses pointers so a caller can flip one switch without
// resetting the others. abort_after_deltas: -1 turns the abort off, 0 or more
// aborts after that many pieces.
type controlRequest struct {
	EmbedFail            *bool             `json:"embed_fail"`
	ChatFail             *bool             `json:"chat_fail"`
	OutOfRangeCitation   *bool             `json:"out_of_range_citation"`
	FailBeforeFirstDelta *bool             `json:"fail_before_first_delta"`
	AbortAfterDeltas     *int              `json:"abort_after_deltas"`
	ChatText             *string           `json:"chat_text"`
	ToolCalls            *[]forcedToolCall `json:"tool_calls"`
	// ResetToolCalls clears the forced list back to the deterministic default;
	// without it a test could only replace one list with another (design.md
	// Decision 13).
	ResetToolCalls *bool `json:"reset_tool_calls"`
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
	if req.FailBeforeFirstDelta != nil {
		s.failBeforeFirstDelta = *req.FailBeforeFirstDelta
	}
	if req.AbortAfterDeltas != nil {
		s.abortAfterDeltas = *req.AbortAfterDeltas
	}
	if req.ChatText != nil {
		s.chatText = *req.ChatText
	}
	if req.ToolCalls != nil {
		calls := append([]forcedToolCall(nil), (*req.ToolCalls)...)
		s.forcedToolCalls = &calls
	}
	if req.ResetToolCalls != nil && *req.ResetToolCalls {
		s.forcedToolCalls = nil
	}
	resp := map[string]any{
		"embed_fail":              s.embedFail,
		"chat_fail":               s.chatFail,
		"out_of_range_citation":   s.outOfRangeCitation,
		"fail_before_first_delta": s.failBeforeFirstDelta,
		"abort_after_deltas":      s.abortAfterDeltas,
		"chat_text":               s.chatText,
		"tool_calls":              s.forcedToolCalls,
		"once_tool_calls":         s.onceToolCalls,
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
