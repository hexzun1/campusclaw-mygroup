package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"campusclaw/backend/internal/upstream"
)

// These tests run against the deterministic stub gateway from
// cmd/stubgateway (design.md Decision 11), which is what every acceptance run
// uses instead of a real model. Set CAMPUSCLAW_TEST_GATEWAY_URL to enable them,
// e.g. CAMPUSCLAW_TEST_GATEWAY_URL=http://stubgateway:8090.

func stubGatewayURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("CAMPUSCLAW_TEST_GATEWAY_URL")
	if url == "" {
		t.Skip("CAMPUSCLAW_TEST_GATEWAY_URL not set; skipping stub gateway test")
	}
	return strings.TrimRight(url, "/")
}

func stubGatewayDim(t *testing.T) int {
	t.Helper()
	if v := os.Getenv("CAMPUSCLAW_TEST_EMBEDDING_DIM"); v != "" {
		dim, err := strconv.Atoi(v)
		if err != nil || dim <= 0 {
			t.Fatalf("CAMPUSCLAW_TEST_EMBEDDING_DIM=%q is not a positive integer", v)
		}
		return dim
	}
	return 256
}

// setStubControl flips one switch on the stub and restores it afterwards.
func setStubControl(t *testing.T, baseURL string, body map[string]bool) {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode control body: %v", err)
	}
	resp, err := http.Post(baseURL+"/control", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /control: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /control status = %d, want 200", resp.StatusCode)
	}
}

func TestStubGatewayEmbedReturnsConfiguredDimension(t *testing.T) {
	baseURL := stubGatewayURL(t)
	dim := stubGatewayDim(t)

	client := NewEmbeddingClient(EmbeddingConfig{
		BaseURL: baseURL, APIKey: "stub-embedding-key", Model: "stub-embedding",
		Dim: dim, Batch: 32, Timeout: 10 * time.Second,
	})

	vectors, err := client.Embed(context.Background(), []string{"机器学习", "食堂菜谱", "图书馆预约"})
	if err != nil {
		t.Fatalf("Embed against the stub failed: %v", err)
	}
	if len(vectors) != 3 {
		t.Fatalf("got %d vectors, want 3", len(vectors))
	}
	for i, v := range vectors {
		if len(v) != dim {
			t.Fatalf("vector %d has %d dimensions, want %d", i, len(v), dim)
		}
	}
}

func TestStubGatewayEmbedIsDeterministic(t *testing.T) {
	baseURL := stubGatewayURL(t)
	client := NewEmbeddingClient(EmbeddingConfig{
		BaseURL: baseURL, APIKey: "stub-embedding-key", Model: "stub-embedding",
		Dim: stubGatewayDim(t), Batch: 32, Timeout: 10 * time.Second,
	})

	ctx := context.Background()
	first, err := client.EmbedOne(ctx, "同一段文本")
	if err != nil {
		t.Fatalf("first EmbedOne failed: %v", err)
	}
	second, err := client.EmbedOne(ctx, "同一段文本")
	if err != nil {
		t.Fatalf("second EmbedOne failed: %v", err)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("dimension %d differs between identical inputs", i)
		}
	}
}

// TestStubGatewayEmbeddingFailureIsUnavailable is the verify from tasks.md 4.1:
// after toggling the stub's embedding failure the client must return an error
// recognised by errors.Is(err, ErrUnavailable), and the message must not carry
// the address or the key.
func TestStubGatewayEmbeddingFailureIsUnavailable(t *testing.T) {
	baseURL := stubGatewayURL(t)
	const apiKey = "stub-embedding-key"

	client := NewEmbeddingClient(EmbeddingConfig{
		BaseURL: baseURL, APIKey: apiKey, Model: "stub-embedding",
		Dim: stubGatewayDim(t), Batch: 32, Timeout: 10 * time.Second,
	})

	setStubControl(t, baseURL, map[string]bool{"embed_fail": true})
	t.Cleanup(func() { setStubControl(t, baseURL, map[string]bool{"embed_fail": false}) })

	_, err := client.Embed(context.Background(), []string{"这段文本会失败"})
	if err == nil {
		t.Fatal("Embed succeeded while the stub was failing")
	}
	if !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error %v is not recognised by errors.Is(err, upstream.ErrUnavailable)", err)
	}

	message := err.Error()
	for _, forbidden := range []string{"stubgateway", "8090", apiKey, "api-key", "Authorization"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("error %q leaks %q", message, forbidden)
		}
	}
}

func TestStubGatewayRecoversAfterFailureIsCleared(t *testing.T) {
	baseURL := stubGatewayURL(t)
	client := NewEmbeddingClient(EmbeddingConfig{
		BaseURL: baseURL, APIKey: "stub-embedding-key", Model: "stub-embedding",
		Dim: stubGatewayDim(t), Batch: 32, Timeout: 10 * time.Second,
	})
	ctx := context.Background()

	setStubControl(t, baseURL, map[string]bool{"embed_fail": true})
	if _, err := client.Embed(ctx, []string{"x"}); err == nil {
		t.Fatal("Embed succeeded while the stub was failing")
	}

	setStubControl(t, baseURL, map[string]bool{"embed_fail": false})
	if _, err := client.Embed(ctx, []string{"x"}); err != nil {
		t.Fatalf("Embed still fails after the stub recovered: %v", err)
	}
}

func TestStubGatewayWrongDimensionIsUnavailable(t *testing.T) {
	baseURL := stubGatewayURL(t)

	// Ask the stub for 256 dimensions but tell the client to expect 8.
	client := NewEmbeddingClient(EmbeddingConfig{
		BaseURL: baseURL, APIKey: "stub-embedding-key", Model: "stub-embedding",
		Dim: 8, Batch: 32, Timeout: 10 * time.Second,
	})

	_, err := client.Embed(context.Background(), []string{"维度不符"})
	if !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error = %v, want upstream.ErrUnavailable", err)
	}
	if !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("error %q does not name the dimension mismatch", err)
	}
}
