package materials

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
)

// unreadableBody fails the test if the handler tries to read the request body.
type unreadableBody struct{}

func (unreadableBody) Read([]byte) (int, error) {
	return 0, errors.New("the request body must not be read for a rejected role")
}
func (unreadableBody) Close() error { return nil }

// TestReindexRejectsStudentBeforeReadingTheBody covers the ordering rule from
// the "学生与跨班不能重建" scenario: a student gets 403 before the (possibly
// broken) body is consumed, so a malformed body cannot turn into a 400.
func TestReindexRejectsStudentBeforeReadingTheBody(t *testing.T) {
	h := &Handlers{}
	req := httptest.NewRequest(http.MethodPost, "/api/materials/1/reindex", nil)
	req = req.WithContext(httpapi.WithSessionUser(req.Context(), &db.SessionUser{
		UserID: 2, Username: "student_a1", Role: db.RoleStudent, ClassID: 1,
	}))
	req.SetPathValue("id", "1")
	req.Body = unreadableBody{}

	rec := httptest.NewRecorder()
	h.Reindex(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
}
