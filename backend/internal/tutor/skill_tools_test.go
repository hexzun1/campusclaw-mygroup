package tutor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"campusclaw/backend/internal/db"
)

// TestSkillSaveErrorWordingMatchesTheOutsideCheck is spec 「技能对 MCP 工具的
// 引用校验」Scenario 保存期间服务器被删除: the 400 a failed in-transaction
// re-check produces must read like the 400 the outside check produces for the
// same missing server, so the teacher sees one wording, not two.
func TestSkillSaveErrorWordingMatchesTheOutsideCheck(t *testing.T) {
	const want = "服务器“campus-tools”不存在，请先登记并连接"

	// The outside check reaches this wording through skillServerMissingText.
	if got := skillServerMissingText("campus-tools"); got != want {
		t.Errorf("outside wording = %q, want %q", got, want)
	}

	rec := httptest.NewRecorder()
	writeSkillSaveError(rec, db.ErrSkillServerMissing{Name: "campus-tools"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !strings.HasPrefix(body.Error, "字段“工具”的取值") || !strings.Contains(body.Error, want) {
		t.Errorf("mapped 400 = %q, want the field wording plus %q", body.Error, want)
	}

	rec = httptest.NewRecorder()
	writeSkillSaveError(rec, db.ErrSkillServerNotConnected{Name: "campus-tools"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !strings.Contains(body.Error, skillServerNotConnectedText("campus-tools")) {
		t.Errorf("mapped 400 = %q, want %q", body.Error, skillServerNotConnectedText("campus-tools"))
	}
}

// TestSkillServerRefsAreDedupedAndSorted is design.md Decision 11: both refs
// carry only the server name, deduped and sorted, so two concurrent saves of
// the same content take the same locks in the same order.
func TestSkillServerRefsAreDedupedAndSorted(t *testing.T) {
	refs := skillServerRefs([]byte(`["b_tool@server-b","a_tool@server-a","c_tool@server-b","knowledge_search"]`))
	if len(refs) != 2 {
		t.Fatalf("refs = %+v, want 2 (deduped, knowledge_search skipped)", refs)
	}
	if refs[0].Name != "server-a" || refs[1].Name != "server-b" {
		t.Errorf("refs = %+v, want server-a then server-b", refs)
	}
	if got := skillServerRefs([]byte(`[]`)); len(got) != 0 {
		t.Errorf("refs for an empty tool list = %+v, want none", got)
	}
}
