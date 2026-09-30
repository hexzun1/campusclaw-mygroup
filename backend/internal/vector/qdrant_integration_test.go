package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"
)

// These tests run against a live Qdrant, and check the same things the
// acceptance script checks with curl: the payload key set of a stored point,
// the class filter of a search, and the effect of deleting one material's
// vectors. Enable them with
//
//	CAMPUSCLAW_TEST_QDRANT_URL=http://qdrant:6333
//	CAMPUSCLAW_TEST_EMBEDDING_DIM=256
//
// Test class and material ids sit far above anything the application allocates,
// and every fixture is removed again by a material-scoped delete.

const (
	testClassA = 9001
	testClassB = 9002
)

func qdrantTarget(t *testing.T) (baseURL string, dim int) {
	t.Helper()
	baseURL = os.Getenv("CAMPUSCLAW_TEST_QDRANT_URL")
	if baseURL == "" {
		t.Skip("CAMPUSCLAW_TEST_QDRANT_URL not set; skipping Qdrant integration test")
	}
	dim = 256
	if v := os.Getenv("CAMPUSCLAW_TEST_EMBEDDING_DIM"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed <= 0 {
			t.Fatalf("CAMPUSCLAW_TEST_EMBEDDING_DIM=%q is not a positive integer", v)
		}
		dim = parsed
	}
	return baseURL, dim
}

func unitVector(dim int) []float64 {
	vec := make([]float64, dim)
	vec[0] = 1
	return vec
}

// rawJSON performs one request straight against Qdrant, outside the client
// under test, so the assertions do not depend on the code being verified.
func rawJSON(t *testing.T, method, url string, body any) map[string]any {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode %s %s response %s: %v", method, url, raw, err)
	}
	decoded["__raw"] = string(raw)
	decoded["__status"] = float64(resp.StatusCode)
	return decoded
}

func countPoints(t *testing.T, baseURL string, materialID, classID int) int {
	t.Helper()
	body := map[string]any{
		"exact": true,
		"filter": map[string]any{
			"must": []any{
				map[string]any{"key": "material_id", "match": map[string]any{"value": materialID}},
				map[string]any{"key": "class_id", "match": map[string]any{"value": classID}},
			},
		},
	}
	resp := rawJSON(t, http.MethodPost, baseURL+"/collections/"+CollectionName+"/points/count", body)
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected count response: %v", resp)
	}
	count, ok := result["count"].(float64)
	if !ok {
		t.Fatalf("count response has no count: %v", resp)
	}
	return int(count)
}

// cleanUpMaterial removes a fixture's vectors when the test ends.
func cleanUpMaterial(t *testing.T, client *Client, materialID, classID int) {
	t.Helper()
	t.Cleanup(func() {
		if err := client.DeleteByMaterial(context.Background(), materialID, classID); err != nil {
			t.Errorf("cleanup material %d: %v", materialID, err)
		}
	})
}

// TestQdrantPointPayloadKeySet is the verify from tasks.md 4.2: the stored
// point's payload keys are exactly the five agreed ones, and the body text is
// nowhere in the stored point (spec: 向量主键与 payload 约束).
func TestQdrantPointPayloadKeySet(t *testing.T) {
	baseURL, dim := qdrantTarget(t)
	client := NewClient(baseURL, "", dim, 10*time.Second)
	ctx := context.Background()

	const (
		materialID = 990101
		entryID    = 990201
		pointID    = int64(8800000001)
	)
	cleanUpMaterial(t, client, materialID, testClassA)

	if err := client.Upsert(ctx, []Point{{
		ID:     pointID,
		Vector: unitVector(dim),
		Payload: PointPayload{
			ClassID: testClassA, MaterialID: materialID,
			KnowledgeEntryID: entryID, ChunkID: pointID, ChunkIndex: 0,
		},
	}}); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	resp := rawJSON(t, http.MethodGet,
		baseURL+"/collections/"+CollectionName+"/points/"+strconv.FormatInt(pointID, 10), nil)
	if resp["__status"] != float64(http.StatusOK) {
		t.Fatalf("point lookup returned %v, want 200", resp["__status"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("point lookup has no result: %v", resp)
	}
	payload, ok := result["payload"].(map[string]any)
	if !ok {
		t.Fatalf("point has no payload: %v", result)
	}

	want := []string{"class_id", "material_id", "knowledge_entry_id", "chunk_id", "chunk_index"}
	if len(payload) != len(want) {
		t.Fatalf("payload has %d keys (%v), want exactly %v", len(payload), payload, want)
	}
	for _, key := range want {
		if _, ok := payload[key]; !ok {
			t.Fatalf("payload is missing %s: %v", key, payload)
		}
	}
	for _, forbidden := range []string{"chunk_text", "body", "text", "excerpt", "vector"} {
		if _, ok := payload[forbidden]; ok {
			t.Fatalf("payload carries %q; the vector store must not hold the body", forbidden)
		}
	}
	if raw, _ := resp["__raw"].(string); len(raw) == 0 {
		t.Fatal("no raw response captured")
	}
}

// TestQdrantSearchIsClassFiltered covers "用 A 班 filter 检索不返回 B 班的点".
func TestQdrantSearchIsClassFiltered(t *testing.T) {
	baseURL, dim := qdrantTarget(t)
	client := NewClient(baseURL, "", dim, 10*time.Second)
	ctx := context.Background()

	const (
		materialA = 990301
		materialB = 990302
		pointA    = int64(8800000011)
		pointB    = int64(8800000012)
	)
	cleanUpMaterial(t, client, materialA, testClassA)
	cleanUpMaterial(t, client, materialB, testClassB)

	if err := client.Upsert(ctx, []Point{
		{ID: pointA, Vector: unitVector(dim), Payload: PointPayload{
			ClassID: testClassA, MaterialID: materialA, KnowledgeEntryID: 1, ChunkID: pointA}},
		{ID: pointB, Vector: unitVector(dim), Payload: PointPayload{
			ClassID: testClassB, MaterialID: materialB, KnowledgeEntryID: 2, ChunkID: pointB}},
	}); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	hitsA, err := client.Search(ctx, testClassA, unitVector(dim), SearchLimit)
	if err != nil {
		t.Fatalf("search class A failed: %v", err)
	}
	if !containsPoint(hitsA, pointA) {
		t.Fatalf("class A search did not return its own point %d: %+v", pointA, hitsA)
	}
	if containsPoint(hitsA, pointB) {
		t.Fatalf("class A search returned class B's point %d", pointB)
	}

	hitsB, err := client.Search(ctx, testClassB, unitVector(dim), SearchLimit)
	if err != nil {
		t.Fatalf("search class B failed: %v", err)
	}
	if !containsPoint(hitsB, pointB) {
		t.Fatalf("class B search did not return its own point %d: %+v", pointB, hitsB)
	}
	if containsPoint(hitsB, pointA) {
		t.Fatalf("class B search returned class A's point %d", pointA)
	}
}

func containsPoint(points []ScoredPoint, id int64) bool {
	for _, p := range points {
		if p.ID == id {
			return true
		}
	}
	return false
}

// TestQdrantDeleteByMaterialRemovesOnlyThatClass covers "按材料删除后该材料的点数为 0".
func TestQdrantDeleteByMaterialRemovesOnlyThatClass(t *testing.T) {
	baseURL, dim := qdrantTarget(t)
	client := NewClient(baseURL, "", dim, 10*time.Second)
	ctx := context.Background()

	const (
		materialA = 990401
		materialB = 990402
	)
	cleanUpMaterial(t, client, materialA, testClassA)
	cleanUpMaterial(t, client, materialB, testClassB)

	points := []Point{}
	for i := 0; i < 3; i++ {
		points = append(points, Point{
			ID:     int64(8800000020 + i),
			Vector: unitVector(dim),
			Payload: PointPayload{
				ClassID: testClassA, MaterialID: materialA,
				KnowledgeEntryID: 1, ChunkID: int64(8800000020 + i), ChunkIndex: i,
			},
		})
	}
	points = append(points, Point{
		ID:     int64(8800000030),
		Vector: unitVector(dim),
		Payload: PointPayload{
			ClassID: testClassB, MaterialID: materialB,
			KnowledgeEntryID: 2, ChunkID: 8800000030, ChunkIndex: 0,
		},
	})
	if err := client.Upsert(ctx, points); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}
	if got := countPoints(t, baseURL, materialA, testClassA); got != 3 {
		t.Fatalf("material A has %d points before the delete, want 3", got)
	}

	if err := client.DeleteByMaterial(ctx, materialA, testClassA); err != nil {
		t.Fatalf("DeleteByMaterial failed: %v", err)
	}

	if got := countPoints(t, baseURL, materialA, testClassA); got != 0 {
		t.Fatalf("material A still has %d points after the delete, want 0", got)
	}
	if got := countPoints(t, baseURL, materialB, testClassB); got != 1 {
		t.Fatalf("the delete reached class B: %d points remain, want 1", got)
	}
}
