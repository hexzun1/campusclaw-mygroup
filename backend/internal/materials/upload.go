package materials

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
)

var allowedExtensions = map[string]bool{
	".txt": true,
	".md":  true,
}

type UploadConfig struct {
	UploadDir      string
	MaxUploadBytes int64
}

func (h *Handlers) Upload(cfg UploadConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		su := httpapi.SessionUserFromContext(r.Context())

		// Role check MUST happen before the request body is read at all:
		// no disk write, no DB write for a rejected student upload.
		if su.Role != db.RoleTeacher {
			httpapi.WriteError(w, http.StatusForbidden, httpapi.MsgForbidden)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxUploadBytes)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				httpapi.WriteError(w, http.StatusRequestEntityTooLarge, "文件超过大小上限")
				return
			}
			httpapi.WriteError(w, http.StatusBadRequest, "请求格式错误")
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "缺少上传文件")
			return
		}
		defer file.Close()

		ext := strings.ToLower(filepath.Ext(header.Filename))
		if !allowedExtensions[ext] {
			httpapi.WriteError(w, http.StatusBadRequest, "不支持的文件类型")
			return
		}

		content, err := io.ReadAll(file)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				httpapi.WriteError(w, http.StatusRequestEntityTooLarge, "文件超过大小上限")
				return
			}
			httpapi.WriteError(w, http.StatusBadRequest, "读取文件失败")
			return
		}
		if len(content) == 0 {
			httpapi.WriteError(w, http.StatusBadRequest, "文件内容为空")
			return
		}
		if !utf8.Valid(content) {
			httpapi.WriteError(w, http.StatusBadRequest, "文件内容必须是合法的 UTF-8 编码")
			return
		}

		title := r.FormValue("title")
		if title == "" {
			base := filepath.Base(header.Filename)
			title = strings.TrimSuffix(base, filepath.Ext(base))
		}

		storedName, err := randomFilename(ext)
		if err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, "内部错误")
			return
		}

		classDir := filepath.Join(cfg.UploadDir, strconv.Itoa(su.ClassID))
		if err := os.MkdirAll(classDir, 0o755); err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, "内部错误")
			return
		}
		fullPath := filepath.Join(classDir, storedName)
		if err := os.WriteFile(fullPath, content, 0o644); err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, "内部错误")
			return
		}

		materialID, err := db.InsertMaterialWithKnowledge(r.Context(), h.Conn, su.ClassID, title, storedName, header.Filename, int64(len(content)), su.UserID, string(content))
		if err != nil {
			_ = os.Remove(fullPath)
			httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
			return
		}

		httpapi.WriteJSON(w, http.StatusCreated, map[string]any{
			"id":    materialID,
			"title": title,
		})
	}
}

func randomFilename(ext string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf) + ext, nil
}
