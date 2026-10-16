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
//
// @Summary 下载材料原文
// @Description 以附件形式返回上传时的原始文件；不存在的编号与他班编号都是同一个 404。
// @Tags 材料
// @Produce octet-stream
// @Param id path int true "材料编号"
// @Success 200 {file} file "原始文件"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 404 {object} httpapi.errorBody "记录不存在（含他班）"
// @Security BearerAuth
// @Router /api/materials/{id}/file [get]
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
