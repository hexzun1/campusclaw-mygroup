package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"campusclaw/backend/internal/upstream"
)

// fakeEmbeddingGateway is a programmable stand-in that records the batches it
// received and can misbehave in the ways the real gateway might.
type fakeEmbeddingGateway struct {
	mu        sync.Mutex
	batches   [][]string
	dim       int
	status    int
	reverse   bool
	duplicate bool
	truncated bool
}

func (f *fakeEmbeddingGateway) handler(w http.ResponseWriter, r *http.Request) {
	var req embeddingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.batches = append(f.batches, req.Input)
	status, dim, reverse, duplicate, truncated := f.status, f.dim, f.reverse, f.duplicate, f.truncated
	f.mu.Unlock()

	if status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"simulated upstream failure"}`))
		return
	}

	count := len(req.Input)
	if truncated && count > 0 {
		count--
	}
	data := make([]map[string]any, 0, count)
	for i := 0; i < count; i++ {
		index := i
		if reverse {
			index = count - 1 - i
		}
		if duplicate {
			index = 0
		}
		vec := make([]float64, dim)
		if dim > 0 {
			vec[0] = float64(index)
		}
		data = append(data, map[string]any{"index": index, "embedding": vec})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func newFakeGateway(t *testing.T, dim int) (*httptest.Server, *fakeEmbeddingGateway) {
	t.Helper()
	fake := &fakeEmbeddingGateway{dim: dim}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(srv.Close)
	return srv, fake
}

func (f *fakeEmbeddingGateway) receivedBatches() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.batches))
	copy(out, f.batches)
	return out
}

const testAPIKey = "super-secret-embedding-key"

func TestEmbedReturnsOneVectorPerInputWithConfiguredDim(t *testing.T) {
	srv, _ := newFakeGateway(t, 4)
	client := NewEmbeddingClient(EmbeddingConfig{
		BaseURL: srv.URL, APIKey: testAPIKey, Model: "test-model", Dim: 4, Batch: 32, Timeout: 5 * time.Second,
	})

	vectors, err := client.Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(vectors) != 3 {
		t.Fatalf("got %d vectors, want 3", len(vectors))
	}
	for i, v := range vectors {
		if len(v) != 4 {
			t.Fatalf("vector %d has %d dimensions, want 4", i, len(v))
		}
	}
}

func TestEmbedSplitsIntoBatches(t *testing.T) {
	srv, fake := newFakeGateway(t, 2)
	client := NewEmbeddingClient(EmbeddingConfig{
		BaseURL: srv.URL, Model: "m", Dim: 2, Batch: 2, Timeout: 5 * time.Second,
	})

	if _, err := client.Embed(context.Background(), []string{"1", "2", "3", "4", "5"}); err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	batches := fake.receivedBatches()
	if len(batches) != 3 {
		t.Fatalf("got %d requests, want 3 (2+2+1)", len(batches))
	}
	for i, want := range []int{2, 2, 1} {
		if len(batches[i]) != want {
			t.Fatalf("batch %d had %d inputs, want %d", i, len(batches[i]), want)
		}
	}
}

func TestEmbedRestoresInputOrderFromIndices(t *testing.T) {
	srv, fake := newFakeGateway(t, 4)
	fake.reverse = true

	client := NewEmbeddingClient(EmbeddingConfig{BaseURL: srv.URL, Model: "m", Dim: 4, Batch: 8})
	vectors, err := client.Embed(context.Background(), []string{"first", "second", "third"})
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	for i, v := range vectors {
		if v[0] != float64(i) {
			t.Fatalf("vector %d marks index %v, want %d", i, v[0], i)
		}
	}
}

func TestEmbedRejectsWrongDimension(t *testing.T) {
	srv, _ := newFakeGateway(t, 8)
	client := NewEmbeddingClient(EmbeddingConfig{BaseURL: srv.URL, Model: "m", Dim: 4, Batch: 8})

	_, err := client.Embed(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("Embed accepted vectors of the wrong dimension")
	}
	if !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error %v does not wrap upstream.ErrUnavailable", err)
	}
	if !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("error %q does not explain the dimension mismatch", err)
	}
}

func TestEmbedRejectsShortResponse(t *testing.T) {
	srv, fake := newFakeGateway(t, 2)
	fake.truncated = true

	client := NewEmbeddingClient(EmbeddingConfig{BaseURL: srv.URL, Model: "m", Dim: 2, Batch: 8})
	if _, err := client.Embed(context.Background(), []string{"a", "b"}); !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error = %v, want upstream.ErrUnavailable", err)
	}
}

func TestEmbedRejectsMissingVector(t *testing.T) {
	srv, fake := newFakeGateway(t, 2)
	fake.duplicate = true

	client := NewEmbeddingClient(EmbeddingConfig{BaseURL: srv.URL, Model: "m", Dim: 2, Batch: 8})
	if _, err := client.Embed(context.Background(), []string{"a", "b"}); !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error = %v, want upstream.ErrUnavailable", err)
	}
}

// TestEmbedSanitizesUpstreamFailures is the "no address, no key" requirement.
func TestEmbedSanitizesUpstreamFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"HTTP 500", http.StatusInternalServerError},
		{"HTTP 401", http.StatusUnauthorized},
		{"HTTP 429", http.StatusTooManyRequests},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, fake := newFakeGateway(t, 2)
			fake.status = tc.status

			client := NewEmbeddingClient(EmbeddingConfig{
				BaseURL: srv.URL, APIKey: testAPIKey, Model: "m", Dim: 2, Batch: 8,
			})
			_, err := client.Embed(context.Background(), []string{"a"})
			if !errors.Is(err, upstream.ErrUnavailable) {
				t.Fatalf("error = %v, want upstream.ErrUnavailable", err)
			}

			message := err.Error()
			for _, forbidden := range []string{"127.0.0.1", srv.URL, testAPIKey, "Authorization", "Bearer"} {
				if strings.Contains(message, forbidden) {
					t.Fatalf("error %q leaks %q", message, forbidden)
				}
			}
		})
	}
}

func TestEmbedSanitizesUnreachableGateway(t *testing.T) {
	srv, _ := newFakeGateway(t, 2)
	client := NewEmbeddingClient(EmbeddingConfig{
		BaseURL: srv.URL, APIKey: testAPIKey, Model: "m", Dim: 2, Batch: 8, Timeout: 2 * time.Second,
	})
	srv.Close() // nothing is listening any more

	_, err := client.Embed(context.Background(), []string{"a"})
	if !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error = %v, want upstream.ErrUnavailable", err)
	}
	for _, forbidden := range []string{"127.0.0.1", testAPIKey, "dial", "connect"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error %q leaks %q", err.Error(), forbidden)
		}
	}
}

func TestEmbedKeepsCauseForLogs(t *testing.T) {
	srv, _ := newFakeGateway(t, 2)
	client := NewEmbeddingClient(EmbeddingConfig{BaseURL: srv.URL, Model: "m", Dim: 2, Batch: 8})
	srv.Close()

	_, err := client.Embed(context.Background(), []string{"a"})
	unavailableErr, ok := err.(*upstream.UnavailableError)
	if !ok {
		t.Fatalf("error type = %T, want *upstream.UnavailableError", err)
	}
	if unavailableErr.Cause() == nil {
		t.Fatal("Cause() is nil, want the underlying transport error for logs")
	}
}

func TestEmbedWithNoInputMakesNoRequest(t *testing.T) {
	srv, fake := newFakeGateway(t, 2)
	client := NewEmbeddingClient(EmbeddingConfig{BaseURL: srv.URL, Model: "m", Dim: 2, Batch: 8})

	vectors, err := client.Embed(context.Background(), nil)
	if err != nil {
		t.Fatalf("Embed(nil) failed: %v", err)
	}
	if len(vectors) != 0 {
		t.Fatalf("got %d vectors, want none", len(vectors))
	}
	if got := len(fake.receivedBatches()); got != 0 {
		t.Fatalf("made %d requests, want none", got)
	}
}

func TestEmbedOneRequiresExactlyOneVector(t *testing.T) {
	srv, _ := newFakeGateway(t, 3)
	client := NewEmbeddingClient(EmbeddingConfig{BaseURL: srv.URL, Model: "m", Dim: 3, Batch: 8})

	vector, err := client.EmbedOne(context.Background(), "query text")
	if err != nil {
		t.Fatalf("EmbedOne failed: %v", err)
	}
	if len(vector) != 3 {
		t.Fatalf("vector has %d dimensions, want 3", len(vector))
	}
	if client.Dim() != 3 {
		t.Fatalf("Dim() = %d, want 3", client.Dim())
	}
}

func TestEmbedSendsModelAndBearerToken(t *testing.T) {
	var gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req embeddingRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"index": 0, "embedding": []float64{1}}},
		})
	}))
	t.Cleanup(srv.Close)

	client := NewEmbeddingClient(EmbeddingConfig{
		BaseURL: srv.URL, APIKey: testAPIKey, Model: "my-model", Dim: 1, Batch: 8,
	})
	if _, err := client.Embed(context.Background(), []string{"a"}); err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if gotAuth != "Bearer "+testAPIKey {
		t.Fatalf("Authorization = %q, want a bearer token", gotAuth)
	}
	if gotModel != "my-model" {
		t.Fatalf("model = %q, want my-model", gotModel)
	}
}

func TestNewEmbeddingClientDefaultsBatchSize(t *testing.T) {
	client := NewEmbeddingClient(EmbeddingConfig{BaseURL: "http://example.invalid", Dim: 4, Batch: 0})
	if client.batch != DefaultEmbeddingBatchSize {
		t.Fatalf("batch = %d, want %d", client.batch, DefaultEmbeddingBatchSize)
	}
}
