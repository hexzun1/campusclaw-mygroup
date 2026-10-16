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

// @Summary 上传材料
// @Description 仅教师可用。上传 .txt / .md（UTF-8），同步完成入库与向量索引；索引失败不回滚材料，切片标记 failed，可用重建恢复。切分参数不合法时不留下任何文件或记录。
// @Tags 材料
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "文件（.txt 或 .md，UTF-8，不超过上传上限）"
// @Param title formData string false "标题（缺省为文件名去掉扩展名）"
// @Param strategy formData string false "切分策略：auto（缺省）/ custom / hierarchy"
// @Param chunk_size formData int false "custom 的窗口大小（100–2000）"
// @Param overlap_percent formData int false "custom 的重叠百分比（0–50）"
// @Param remove_url formData boolean false "预处理：移除链接（true/false）"
// @Param remove_email formData boolean false "预处理：移除邮箱（true/false）"
// @Param collapse_whitespace formData boolean false "预处理：合并空白（true/false）"
// @Success 201 {object} map[string]interface{} "id、title、index_status"
// @Failure 400 {object} httpapi.errorBody "文件类型、编码、大小或切分参数不合法"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 403 {object} httpapi.errorBody "学生无权上传"
// @Failure 413 {object} httpapi.errorBody "文件超过大小上限"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/materials [post]
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
		chunks := toDBChunks(knowledge.ChunkBody(body, params))

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

// toDBChunks turns chunker output into storage rows. The identifiers and status
// are assigned by the database layer, so a caller cannot place a chunk in
// another class.
func toDBChunks(produced []knowledge.Chunk) []db.Chunk {
	chunks := make([]db.Chunk, 0, len(produced))
	for _, c := range produced {
		chunks = append(chunks, db.Chunk{
			ChunkIndex: c.Index,
			CharStart:  c.Span.Start,
			CharEnd:    c.Span.End,
			ChunkText:  c.Text,
		})
	}
	return chunks
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
