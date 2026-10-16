import { useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { ApiError, downloadMaterialFile, type MaterialDetail } from '../api/client'

// MaterialDetailView renders one material's title, download button and body.
// Shared by the materials page and the assistant page's citation panel;
// react-markdown runs without raw-HTML support, so a body containing <script>
// stays inert.
export default function MaterialDetailView({ material, onClose }: { material: MaterialDetail; onClose: () => void }) {
  const [error, setError] = useState<string | null>(null)

  // Downloads must carry the Authorization header, so they go through fetch +
  // Blob instead of a plain link.
  async function handleDownload() {
    try {
      await downloadMaterialFile(material.id)
    } catch (err) {
      if (err instanceof ApiError && err.status !== 401) setError(err.message)
    }
  }

  return (
    <div className="material-detail">
      <div className="material-detail-header">
        <h2>{material.title}</h2>
        <div>
          <button type="button" onClick={handleDownload}>
            下载原文件
          </button>
          <button type="button" onClick={onClose}>
            关闭
          </button>
        </div>
      </div>
      {error && <p className="error-text">{error}</p>}
      <div className="markdown-body">
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{material.body}</ReactMarkdown>
      </div>
    </div>
  )
}
