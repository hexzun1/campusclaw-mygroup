// Package vector stores chunk embeddings in Qdrant over its REST API, using
// only the standard library (design.md Decision 3).
//
// The collection is ensured lazily on first use rather than at startup: Qdrant
// can be slower than the api or unavailable entirely, and the api must still
// start and keep serving keyword search in that case.
package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"campusclaw/backend/internal/upstream"
)

// CollectionName is the single collection holding every class's chunk vectors;
// class isolation comes from the payload filter, not from separate collections.
const CollectionName = "campusclaw_chunks"

const (
	// ScoreThreshold drops weak cosine matches (spec: 向量检索).
	ScoreThreshold = 0.35
	// SearchLimit caps the candidates one search returns.
	SearchLimit = 50
	// maxPointsPerRequest bounds one upsert payload.
	maxPointsPerRequest = 256
	// maxErrorBodyBytes is how much of an error response is drained so the
	// connection can be reused. Its content is never surfaced.
	maxErrorBodyBytes = 4 << 10
)

// PointPayload is the payload of a vector point. It is a struct on purpose:
// the key set is exactly the five agreed fields, so the chunk text can never
// leak into the vector store (spec: 向量主键与 payload 约束).
type PointPayload struct {
	ClassID          int   `json:"class_id"`
	MaterialID       int   `json:"material_id"`
	KnowledgeEntryID int   `json:"knowledge_entry_id"`
	ChunkID          int64 `json:"chunk_id"`
	ChunkIndex       int   `json:"chunk_index"`
}

// Point is one chunk's vector entry. The point ID is the chunk's MySQL primary
// key, so a hit can be looked up in MySQL directly.
type Point struct {
	ID      int64
	Vector  []float64
	Payload PointPayload
}

// ScoredPoint is one search result.
type ScoredPoint struct {
	ID      int64
	Score   float64
	Payload PointPayload
}

// Client is a minimal Qdrant REST client.
type Client struct {
	baseURL string
	apiKey  string
	dim     int
	http    *http.Client

	mu      sync.Mutex
	ensured bool
}

// NewClient builds the client. A non-positive timeout becomes 30 seconds.
func NewClient(baseURL, apiKey string, dim int, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		dim:     dim,
		http:    &http.Client{Timeout: timeout},
	}
}

// Dim is the configured vector size.
func (c *Client) Dim() int { return c.dim }

// Ensure creates the collection and its payload indexes when they do not exist
// yet, and verifies the vector size matches EMBEDDING_DIM.
//
// An existing collection whose size differs is reported as a dependency
// failure: the collection is never deleted automatically, because its vectors
// are still the record of what was indexed (design.md Decision 3).
func (c *Client) Ensure(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ensured {
		return nil
	}

	var info collectionInfo
	status, err := c.do(ctx, http.MethodGet, "/collections/"+CollectionName, nil, &info)
	switch {
	case err == nil:
		size := info.Result.Config.Params.Vectors.Size
		if size != c.dim {
			return upstream.Unavailable(fmt.Sprintf(
				"the vector collection holds %d-dimensional vectors but EMBEDDING_DIM is %d",
				size, c.dim), nil)
		}
	case status == http.StatusNotFound:
		if err := c.createCollection(ctx); err != nil {
			return err
		}
	default:
		return err
	}

	for _, field := range []string{"class_id", "material_id"} {
		if err := c.createPayloadIndex(ctx, field); err != nil {
			return err
		}
	}

	c.ensured = true
	return nil
}

func (c *Client) createCollection(ctx context.Context) error {
	body := map[string]any{
		"vectors": map[string]any{"size": c.dim, "distance": "Cosine"},
	}
	if _, err := c.do(ctx, http.MethodPut, "/collections/"+CollectionName, body, nil); err != nil {
		return upstream.Unavailable("the vector collection could not be created", causeOf(err))
	}
	return nil
}

func (c *Client) createPayloadIndex(ctx context.Context, field string) error {
	body := map[string]any{"field_name": field, "field_schema": "integer"}
	path := "/collections/" + CollectionName + "/index?wait=true"
	if _, err := c.do(ctx, http.MethodPut, path, body, nil); err != nil {
		return upstream.Unavailable("a payload index could not be created", causeOf(err))
	}
	return nil
}

// Upsert writes points with wait=true, so a returned nil means the vectors are
// already searchable.
func (c *Client) Upsert(ctx context.Context, points []Point) error {
	if len(points) == 0 {
		return nil
	}
	if err := c.Ensure(ctx); err != nil {
		return err
	}

	for start := 0; start < len(points); start += maxPointsPerRequest {
		end := min(start+maxPointsPerRequest, len(points))
		batch := points[start:end]

		wire := make([]map[string]any, 0, len(batch))
		for _, p := range batch {
			if len(p.Vector) != c.dim {
				return upstream.Unavailable(fmt.Sprintf(
					"a point carries %d dimensions but EMBEDDING_DIM is %d", len(p.Vector), c.dim), nil)
			}
			wire = append(wire, map[string]any{
				"id":      p.ID,
				"vector":  p.Vector,
				"payload": p.Payload,
			})
		}

		path := "/collections/" + CollectionName + "/points?wait=true"
		if _, err := c.do(ctx, http.MethodPut, path, map[string]any{"points": wire}, nil); err != nil {
			return upstream.Unavailable("the vectors could not be written", causeOf(err))
		}
	}
	return nil
}

// Search returns the closest chunks of classID, dropping matches whose cosine
// score is below ScoreThreshold. The class filter is applied by the vector
// store itself, which is the first of the three isolation layers.
func (c *Client) Search(ctx context.Context, classID int, vector []float64, limit int) ([]ScoredPoint, error) {
	if len(vector) != c.dim {
		return nil, upstream.Unavailable(fmt.Sprintf(
			"the query vector has %d dimensions but EMBEDDING_DIM is %d", len(vector), c.dim), nil)
	}
	if limit <= 0 {
		limit = SearchLimit
	}
	if err := c.Ensure(ctx); err != nil {
		return nil, err
	}

	body := map[string]any{
		"vector":          vector,
		"limit":           limit,
		"score_threshold": ScoreThreshold,
		"with_payload":    true,
		"filter": map[string]any{
			"must": []any{
				map[string]any{"key": "class_id", "match": map[string]any{"value": classID}},
			},
		},
	}

	var resp searchResponse
	path := "/collections/" + CollectionName + "/points/search"
	if _, err := c.do(ctx, http.MethodPost, path, body, &resp); err != nil {
		return nil, err
	}

	out := make([]ScoredPoint, 0, len(resp.Result))
	for _, item := range resp.Result {
		out = append(out, ScoredPoint{ID: item.ID, Score: item.Score, Payload: item.Payload})
	}
	return out, nil
}

// DeleteByMaterial removes every vector of one material inside one class. The
// class condition means a delete can never reach another class's points.
func (c *Client) DeleteByMaterial(ctx context.Context, materialID, classID int) error {
	body := map[string]any{
		"filter": map[string]any{
			"must": []any{
				map[string]any{"key": "material_id", "match": map[string]any{"value": materialID}},
				map[string]any{"key": "class_id", "match": map[string]any{"value": classID}},
			},
		},
	}

	path := "/collections/" + CollectionName + "/points/delete?wait=true"
	if _, err := c.do(ctx, http.MethodPost, path, body, nil); err != nil {
		return upstream.Unavailable("the vectors could not be deleted", causeOf(err))
	}
	return nil
}

type collectionInfo struct {
	Result struct {
		Config struct {
			Params struct {
				Vectors struct {
					Size     int    `json:"size"`
					Distance string `json:"distance"`
				} `json:"vectors"`
			} `json:"params"`
		} `json:"config"`
	} `json:"result"`
}

type searchResponse struct {
	Result []struct {
		ID      int64        `json:"id"`
		Score   float64      `json:"score"`
		Payload PointPayload `json:"payload"`
	} `json:"result"`
}

// do performs one request. It returns the HTTP status alongside the error so a
// caller can treat 404 specially; for a non-2xx response the error is a
// sanitized ErrUnavailable that never names the vector store's address.
func (c *Client) do(ctx context.Context, method, path string, payload, out any) (int, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, upstream.Unavailable("could not encode the vector store request", err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return 0, upstream.Unavailable("could not build the vector store request", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("api-key", c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, upstream.Unavailable("the vector store is unreachable", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodyBytes))
		return resp.StatusCode, upstream.Unavailable(
			fmt.Sprintf("the vector store answered HTTP %d", resp.StatusCode), nil)
	}

	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodyBytes))
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return resp.StatusCode, upstream.Unavailable("the vector store response could not be decoded", err)
	}
	return resp.StatusCode, nil
}

// causeOf recovers the transport cause for a log line while the message that
// reaches a client stays sanitized.
func causeOf(err error) error {
	if unavailableErr, ok := err.(*upstream.UnavailableError); ok {
		return unavailableErr.Cause()
	}
	return err
}
