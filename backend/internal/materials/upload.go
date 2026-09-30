package materials

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
	"campusclaw/backend/internal/knowledge"
)

var allowedExtensions = map[string]bool{
	".txt": true,
	".md":  true,
}

type UploadConfig struct {
	UploadDir      string
	MaxUploadBytes int64
}

// uploadParamNames are the chunking parameters an upload may carry. They are
// collected from the multipart form before anything is written, so an invalid
// value cannot leave a file or a row behind (spec: 切分参数不合法).
var uploadParamNames = []string{
	knowledge.ParamStrategy,
	knowledge.ParamChunkSize,
	knowledge.ParamOverlapPercent,
	knowledge.ParamRemoveURL,
	knowledge.ParamRemoveEmail,
	knowledge.ParamCollapseWhitespace,
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

		// Chunking parameters are validated before the file is written, so a bad
		// strategy or an out-of-range size leaves the disk and the tables
		// untouched.
		params, err := chunkParamsFromForm(r.MultipartForm)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "切分参数不合法："+err.Error())
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

		body := string(content)
		produced := knowledge.ChunkBody(body, params)
		chunks := make([]db.Chunk, 0, len(produced))
		for _, c := range produced {
			chunks = append(chunks, db.Chunk{
				ChunkIndex: c.Index,
				CharStart:  c.Span.Start,
				CharEnd:    c.Span.End,
				ChunkText:  c.Text,
			})
		}

		materialID, err := db.InsertMaterialWithChunks(r.Context(), h.Conn, db.NewMaterial{
			ClassID:       su.ClassID,
			Title:         title,
			StoredName:    storedName,
			OriginalName:  header.Filename,
			SizeBytes:     int64(len(content)),
			UploadedBy:    su.UserID,
			BodyText:      body,
			ChunkStrategy: string(params.Strategy),
			ChunkParams:   params.ParamsJSON(),
			Chunks:        chunks,
		})
		if err != nil {
			_ = os.Remove(fullPath)
			httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
			return
		}

		// Indexing runs after the commit. Its failure never rolls the material
		// back; it only leaves the affected chunks marked as failed, which a
		// reindex can repair (design.md Decision 6).
		status := db.IndexPending
		if h.Indexer != nil {
			if err := h.Indexer.IndexMaterial(r.Context(), materialID, su.ClassID); err != nil {
				log.Printf("upload: index material %d: %v", materialID, err)
			}
		}
		if stats, err := db.GetChunkStats(r.Context(), h.Conn, materialID, su.ClassID); err == nil {
			status = stats.Status
		}

		httpapi.WriteJSON(w, http.StatusCreated, map[string]any{
			"id":           materialID,
			"title":        title,
			"index_status": status,
		})
	}
}

// chunkParamsFromForm reads only the chunking parameters the client actually
// sent: a missing key keeps its default, while a present but empty value is
// invalid rather than silently ignored.
func chunkParamsFromForm(form *multipart.Form) (knowledge.ChunkParams, error) {
	raw := map[string]string{}
	if form != nil {
		for _, name := range uploadParamNames {
			if values, ok := form.Value[name]; ok && len(values) > 0 {
				raw[name] = values[0]
			}
		}
	}
	return knowledge.ParseParams(raw)
}

func randomFilename(ext string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf) + ext, nil
}
