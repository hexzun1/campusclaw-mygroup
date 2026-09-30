package gateway

import (
	"context"
	"fmt"
	"time"

	"campusclaw/backend/internal/upstream"
)

// DefaultEmbeddingBatchSize mirrors the optional EMBEDDING_BATCH_SIZE default.
const DefaultEmbeddingBatchSize = 32

// EmbeddingConfig configures the embedding gateway client.
type EmbeddingConfig struct {
	BaseURL string
	APIKey  string
	Model   string
	// Dim is EMBEDDING_DIM: every returned vector must have exactly this many
	// components, otherwise the collection and the vectors would disagree.
	Dim     int
	Batch   int
	Timeout time.Duration
}

// EmbeddingClient turns text into vectors through POST {base}/embeddings.
type EmbeddingClient struct {
	client *Client
	dim    int
	batch  int
}

// NewEmbeddingClient builds the client. A non-positive batch size becomes 32.
func NewEmbeddingClient(cfg EmbeddingConfig) *EmbeddingClient {
	batch := cfg.Batch
	if batch <= 0 {
		batch = DefaultEmbeddingBatchSize
	}
	return &EmbeddingClient{
		client: NewClient(cfg.BaseURL, cfg.APIKey, cfg.Model, cfg.Timeout),
		dim:    cfg.Dim,
		batch:  batch,
	}
}

// Dim is the configured embedding dimension.
func (c *EmbeddingClient) Dim() int { return c.dim }

// Embed returns one vector per input text, in input order. Inputs are sent in
// batches of at most EMBEDDING_BATCH_SIZE.
//
// Any failure — unreachable gateway, non-2xx status, undecodable body, a
// missing vector or one whose length is not Dim — is reported as
// ErrUnavailable, because none of them is actionable differently by a caller.
func (c *EmbeddingClient) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	vectors := make([][]float64, 0, len(texts))
	for start := 0; start < len(texts); start += c.batch {
		end := min(start+c.batch, len(texts))
		batch := texts[start:end]

		var resp embeddingResponse
		if err := c.client.post(ctx, "/embeddings", embeddingRequest{
			Model: c.client.Model(),
			Input: batch,
		}, &resp); err != nil {
			return nil, err
		}

		batchVectors, err := c.vectorsFor(batch, &resp)
		if err != nil {
			return nil, err
		}
		vectors = append(vectors, batchVectors...)
	}
	return vectors, nil
}

// EmbedOne is the convenience form used by query embedding.
func (c *EmbeddingClient) EmbedOne(ctx context.Context, text string) ([]float64, error) {
	vectors, err := c.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 {
		return nil, upstream.Unavailable("the gateway did not return exactly one vector", nil)
	}
	return vectors[0], nil
}

type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

// vectorsFor validates one batch's response and restores the input order via
// each item's index.
func (c *EmbeddingClient) vectorsFor(batch []string, resp *embeddingResponse) ([][]float64, error) {
	if len(resp.Data) != len(batch) {
		return nil, upstream.Unavailable(fmt.Sprintf(
			"the embedding gateway returned %d vectors for %d inputs", len(resp.Data), len(batch)), nil)
	}

	vectors := make([][]float64, len(batch))
	for _, item := range resp.Data {
		if item.Index < 0 || item.Index >= len(batch) {
			return nil, upstream.Unavailable("the embedding gateway returned an out-of-range index", nil)
		}
		if len(item.Embedding) != c.dim {
			return nil, upstream.Unavailable(fmt.Sprintf(
				"the embedding gateway returned %d dimensions but EMBEDDING_DIM is %d",
				len(item.Embedding), c.dim), nil)
		}
		vectors[item.Index] = item.Embedding
	}
	for i, v := range vectors {
		if v == nil {
			return nil, upstream.Unavailable(fmt.Sprintf("the embedding gateway skipped input %d", i), nil)
		}
	}
	return vectors, nil
}
