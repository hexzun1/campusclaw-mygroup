package vector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"campusclaw/backend/internal/upstream"
)

type recordedRequest struct {
	method string
	path   string
	body   []byte
}

// fakeQdrant records what the client sent and can pretend the collection is
// missing, present with a given size, or failing.
type fakeQdrant struct {
	mu sync.Mutex

	records []recordedRequest

	collectionExists bool
	collectionDim    int
	failStatus       int
	failPath         string
	searchResult     string
}

func newFakeQdrant(t *testing.T, exists bool, dim int) (*httptest.Server, *fakeQdrant) {
	t.Helper()
	fake := &fakeQdrant{collectionExists: exists, collectionDim: dim}
	srv := httptest.NewServer(http.HandlerFunc(fake.handle))
	t.Cleanup(srv.Close)
	return srv, fake
}

func (f *fakeQdrant) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	f.mu.Lock()
	f.records = append(f.records, recordedRequest{method: r.Method, path: r.URL.Path, body: body})
	exists, dim, failStatus, failPath, searchResult := f.collectionExists, f.collectionDim, f.failStatus, f.failPath, f.searchResult
	f.mu.Unlock()

	if failStatus != 0 && strings.HasPrefix(r.URL.Path, failPath) {
		w.WriteHeader(failStatus)
		_, _ = w.Write([]byte(`{"status":{"error":"simulated"}}`))
		return
	}

	collectionPath := "/collections/" + CollectionName
	switch {
	case r.Method == http.MethodGet && r.URL.Path == collectionPath:
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":{"error":"Not found"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"result":{"config":{"params":{"vectors":{"size":` +
			strconv.Itoa(dim) + `,"distance":"Cosine"}}}}}`))
	case r.Method == http.MethodPost && r.URL.Path == collectionPath+"/points/search":
		if searchResult != "" {
			_, _ = w.Write([]byte(searchResult))
			return
		}
		_, _ = w.Write([]byte(`{"result":[]}`))
	default:
		_, _ = w.Write([]byte(`{"result":[]}`))
	}
}

// recordsFor returns every recorded request for a method and path.
func (f *fakeQdrant) recordsFor(method, path string) []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recordedRequest
	for _, rec := range f.records {
		if rec.method == method && rec.path == path {
			out = append(out, rec)
		}
	}
	return out
}

// bodyFor decodes the most recent body sent to a method and path.
func (f *fakeQdrant) bodyFor(method, path string) map[string]any {
	records := f.recordsFor(method, path)
	if len(records) == 0 {
		return nil
	}
	var decoded map[string]any
	_ = json.Unmarshal(records[len(records)-1].body, &decoded)
	return decoded
}

func (f *fakeQdrant) saw(method, path string) bool {
	return len(f.recordsFor(method, path)) > 0
}

func (f *fakeQdrant) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.records)
}

func TestEnsureCreatesCollectionWithConfiguredDim(t *testing.T) {
	srv, fake := newFakeQdrant(t, false, 0)
	client := NewClient(srv.URL, "", 8, 5*time.Second)

	if err := client.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}

	body := fake.bodyFor(http.MethodPut, "/collections/"+CollectionName)
	if body == nil {
		t.Fatal("the collection was never created")
	}
	vectors, ok := body["vectors"].(map[string]any)
	if !ok {
		t.Fatalf("create body has no vectors object: %v", body)
	}
	if vectors["size"] != float64(8) {
		t.Fatalf("created collection size = %v, want 8", vectors["size"])
	}
	if vectors["distance"] != "Cosine" {
		t.Fatalf("created collection distance = %v, want Cosine", vectors["distance"])
	}

	indexRecords := fake.recordsFor(http.MethodPut, "/collections/"+CollectionName+"/index")
	indexed := map[string]bool{}
	for _, rec := range indexRecords {
		var payload map[string]any
		if err := json.Unmarshal(rec.body, &payload); err != nil {
			t.Fatalf("decode index body: %v", err)
		}
		field, _ := payload["field_name"].(string)
		indexed[field] = true
	}
	for _, field := range []string{"class_id", "material_id"} {
		if !indexed[field] {
			t.Fatalf("no payload index was created for %s (got %v)", field, indexed)
		}
	}
}

// TestEnsureRejectsDimensionMismatch pins the decision that a collection of the
// wrong size is a dependency failure, never an automatic delete.
func TestEnsureRejectsDimensionMismatch(t *testing.T) {
	srv, fake := newFakeQdrant(t, true, 4)
	client := NewClient(srv.URL, "", 8, 5*time.Second)

	err := client.Ensure(context.Background())
	if !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error = %v, want upstream.ErrUnavailable", err)
	}
	if !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("error %q does not point at the dimension mismatch", err)
	}
	if fake.saw(http.MethodDelete, "/collections/"+CollectionName) {
		t.Fatal("the client deleted the collection instead of reporting the mismatch")
	}
}

func TestEnsureIsCached(t *testing.T) {
	srv, fake := newFakeQdrant(t, true, 8)
	client := NewClient(srv.URL, "", 8, 5*time.Second)

	for i := 0; i < 3; i++ {
		if err := client.Ensure(context.Background()); err != nil {
			t.Fatalf("Ensure %d failed: %v", i, err)
		}
	}
	if got := fake.requestCount(); got != 3 {
		t.Fatalf("made %d requests for three Ensure calls, want 3 (one probe, two indexes)", got)
	}
}

func TestSearchSendsDocumentedBody(t *testing.T) {
	srv, fake := newFakeQdrant(t, true, 4)
	fake.searchResult = `{"result":[{"id":42,"score":0.9,"payload":` +
		`{"class_id":1,"material_id":2,"knowledge_entry_id":3,"chunk_id":42,"chunk_index":0}}]}`

	client := NewClient(srv.URL, "", 4, 5*time.Second)
	points, err := client.Search(context.Background(), 1, []float64{1, 0, 0, 0}, 0)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(points) != 1 || points[0].ID != 42 || points[0].Payload.ClassID != 1 {
		t.Fatalf("Search returned %+v", points)
	}

	body := fake.bodyFor(http.MethodPost, "/collections/"+CollectionName+"/points/search")
	if body["score_threshold"] != ScoreThreshold {
		t.Fatalf("score_threshold = %v, want %v", body["score_threshold"], ScoreThreshold)
	}
	if body["limit"] != float64(SearchLimit) {
		t.Fatalf("limit = %v, want %d", body["limit"], SearchLimit)
	}
	if body["with_payload"] != true {
		t.Fatalf("with_payload = %v, want true", body["with_payload"])
	}
	filter, ok := body["filter"].(map[string]any)
	if !ok {
		t.Fatalf("no filter in the search body: %v", body)
	}
	must, ok := filter["must"].([]any)
	if !ok || len(must) != 1 {
		t.Fatalf("filter.must = %v, want exactly the class condition", filter["must"])
	}
	condition := must[0].(map[string]any)
	if condition["key"] != "class_id" {
		t.Fatalf("filter key = %v, want class_id", condition["key"])
	}
	if match, _ := condition["match"].(map[string]any); match["value"] != float64(1) {
		t.Fatalf("filter value = %v, want 1", condition["match"])
	}
}

func TestSearchSanitizesUpstreamFailure(t *testing.T) {
	srv, fake := newFakeQdrant(t, true, 4)
	fake.failStatus = http.StatusInternalServerError
	fake.failPath = "/collections/" + CollectionName + "/points/search"

	client := NewClient(srv.URL, "qdrant-secret-key", 4, 5*time.Second)
	_, err := client.Search(context.Background(), 1, []float64{1, 0, 0, 0}, SearchLimit)
	if !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error = %v, want upstream.ErrUnavailable", err)
	}
	for _, forbidden := range []string{"127.0.0.1", srv.URL, "qdrant-secret-key", "api-key"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error %q leaks %q", err.Error(), forbidden)
		}
	}
}

func TestSearchRejectsWrongQueryDimensionLocally(t *testing.T) {
	srv, fake := newFakeQdrant(t, true, 4)
	client := NewClient(srv.URL, "", 4, 5*time.Second)

	_, err := client.Search(context.Background(), 1, []float64{1, 0}, SearchLimit)
	if !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error = %v, want upstream.ErrUnavailable", err)
	}
	if fake.requestCount() != 0 {
		t.Fatalf("a malformed query still produced %d requests", fake.requestCount())
	}
}

func TestUpsertRejectsWrongVectorDimension(t *testing.T) {
	srv, fake := newFakeQdrant(t, true, 4)
	client := NewClient(srv.URL, "", 4, 5*time.Second)

	err := client.Upsert(context.Background(), []Point{{
		ID:      1,
		Vector:  []float64{1, 0},
		Payload: PointPayload{ClassID: 1, MaterialID: 1, KnowledgeEntryID: 1, ChunkID: 1},
	}})
	if !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error = %v, want upstream.ErrUnavailable", err)
	}
	if fake.saw(http.MethodPut, "/collections/"+CollectionName+"/points") {
		t.Fatal("a malformed point still reached the vector store")
	}
}

func TestDeleteByMaterialSendsBothConditions(t *testing.T) {
	srv, fake := newFakeQdrant(t, true, 4)
	client := NewClient(srv.URL, "", 4, 5*time.Second)

	if err := client.DeleteByMaterial(context.Background(), 7, 3); err != nil {
		t.Fatalf("DeleteByMaterial failed: %v", err)
	}

	body := fake.bodyFor(http.MethodPost, "/collections/"+CollectionName+"/points/delete")
	if body == nil {
		t.Fatal("no delete request was made")
	}
	filter := body["filter"].(map[string]any)
	must := filter["must"].([]any)
	if len(must) != 2 {
		t.Fatalf("delete filter has %d conditions, want material_id and class_id", len(must))
	}
	conditions := map[string]any{}
	for _, raw := range must {
		condition := raw.(map[string]any)
		conditions[condition["key"].(string)] = condition["match"].(map[string]any)["value"]
	}
	if conditions["material_id"] != float64(7) || conditions["class_id"] != float64(3) {
		t.Fatalf("delete conditions = %v, want material_id=7 and class_id=3", conditions)
	}
}

func TestDeleteByMaterialTreatsMissingCollectionAsNoop(t *testing.T) {
	srv, fake := newFakeQdrant(t, true, 4)
	fake.failStatus = http.StatusNotFound
	fake.failPath = "/collections/" + CollectionName + "/points/delete"

	client := NewClient(srv.URL, "", 4, 5*time.Second)
	if err := client.DeleteByMaterial(context.Background(), 7, 3); err != nil {
		t.Fatalf("a missing collection means nothing to delete, got: %v", err)
	}
}

func TestDeleteByMaterialReportsRealFailures(t *testing.T) {
	srv, fake := newFakeQdrant(t, true, 4)
	fake.failStatus = http.StatusInternalServerError
	fake.failPath = "/collections/" + CollectionName + "/points/delete"

	client := NewClient(srv.URL, "qdrant-secret-key", 4, 5*time.Second)
	err := client.DeleteByMaterial(context.Background(), 7, 3)
	if !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error = %v, want upstream.ErrUnavailable", err)
	}
}

func TestUnreachableVectorStoreIsSanitized(t *testing.T) {
	srv, _ := newFakeQdrant(t, true, 4)
	client := NewClient(srv.URL, "qdrant-secret-key", 4, 2*time.Second)
	srv.Close()

	err := client.Ensure(context.Background())
	if !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("error = %v, want upstream.ErrUnavailable", err)
	}
	for _, forbidden := range []string{"127.0.0.1", "qdrant-secret-key", "dial", "connect"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error %q leaks %q", err.Error(), forbidden)
		}
	}
}

func TestUpsertWithNoPointsMakesNoRequest(t *testing.T) {
	srv, fake := newFakeQdrant(t, true, 4)
	client := NewClient(srv.URL, "", 4, 5*time.Second)

	if err := client.Upsert(context.Background(), nil); err != nil {
		t.Fatalf("Upsert(nil) failed: %v", err)
	}
	if fake.requestCount() != 0 {
		t.Fatalf("Upsert(nil) made %d requests", fake.requestCount())
	}
}

func TestUpsertSendsPointsWithPayload(t *testing.T) {
	srv, fake := newFakeQdrant(t, true, 2)
	client := NewClient(srv.URL, "", 2, 5*time.Second)

	err := client.Upsert(context.Background(), []Point{{
		ID:      99,
		Vector:  []float64{1, 0},
		Payload: PointPayload{ClassID: 1, MaterialID: 2, KnowledgeEntryID: 3, ChunkID: 99, ChunkIndex: 4},
	}})
	if err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	body := fake.bodyFor(http.MethodPut, "/collections/"+CollectionName+"/points")
	points, ok := body["points"].([]any)
	if !ok || len(points) != 1 {
		t.Fatalf("upsert body = %v, want one point", body)
	}
	point := points[0].(map[string]any)
	if point["id"] != float64(99) {
		t.Fatalf("point id = %v, want 99", point["id"])
	}
	payload, ok := point["payload"].(map[string]any)
	if !ok {
		t.Fatalf("point has no payload: %v", point)
	}
	if len(payload) != 5 {
		t.Fatalf("payload has %d keys (%v), want exactly the five agreed ones", len(payload), payload)
	}
	for _, key := range []string{"class_id", "material_id", "knowledge_entry_id", "chunk_id", "chunk_index"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("payload is missing %s: %v", key, payload)
		}
	}
}

func TestPointPayloadHasExactlyTheAgreedKeys(t *testing.T) {
	encoded, err := json.Marshal(PointPayload{
		ClassID: 1, MaterialID: 2, KnowledgeEntryID: 3, ChunkID: 4, ChunkIndex: 5,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	want := []string{"class_id", "material_id", "knowledge_entry_id", "chunk_id", "chunk_index"}
	if len(decoded) != len(want) {
		t.Fatalf("payload has %d keys (%v), want exactly %v", len(decoded), decoded, want)
	}
	for _, key := range want {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("payload is missing %s: %v", key, decoded)
		}
	}
}
