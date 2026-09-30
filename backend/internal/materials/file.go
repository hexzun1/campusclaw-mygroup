package materials

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"campusclaw/backend/internal/httpapi"
)

// File streams the original uploaded file after the same session + class
// check as Detail. Class isolation and not-found both resolve to 404.
func (h *Handlers) File(uploadDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := h.loadOwnClassMaterial(w, r)
		if m == nil {
			return
		}

		fullPath := filepath.Join(uploadDir, strconv.Itoa(m.ClassID), m.StoredName)
		f, err := os.Open(fullPath)
		if err != nil {
			httpapi.WriteError(w, http.StatusNotFound, httpapi.MsgNotFound)
			return
		}
		defer f.Close()

		w.Header().Set("Content-Disposition", contentDisposition(m.OriginalName))
		http.ServeContent(w, r, m.OriginalName, m.CreatedAt, f)
	}
}
