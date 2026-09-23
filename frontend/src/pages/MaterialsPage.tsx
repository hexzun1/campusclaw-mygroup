import { useCallback, useEffect, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import {
  ApiError,
  fileDownloadUrl,
  getMaterial,
  listMaterials,
  uploadMaterial,
  type MaterialDetail,
  type MaterialSummary,
} from '../api/client'
import { useAuth } from '../auth/AuthContext'
import ThemeToggle from '../components/ThemeToggle'

export default function MaterialsPage() {
  const { user, logout } = useAuth()
  const [query, setQuery] = useState('')
  const [materials, setMaterials] = useState<MaterialSummary[]>([])
  const [selected, setSelected] = useState<MaterialDetail | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [uploadMessage, setUploadMessage] = useState<{ ok: boolean; text: string } | null>(null)
  const [uploading, setUploading] = useState(false)

  const refreshList = useCallback(async (q: string) => {
    try {
      const list = await listMaterials(q)
      setMaterials(list)
      setLoadError(null)
    } catch (err) {
      if (err instanceof ApiError && err.status !== 401) {
        setLoadError(err.message)
      }
    }
  }, [])

  useEffect(() => {
    refreshList(query)
  }, [query, refreshList])

  async function openDetail(id: number) {
    try {
      const detail = await getMaterial(id)
      setSelected(detail)
    } catch (err) {
      if (err instanceof ApiError) setLoadError(err.message)
    }
  }

  async function handleUpload(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = e.currentTarget
    const fileInput = form.elements.namedItem('file') as HTMLInputElement
    const titleInput = form.elements.namedItem('title') as HTMLInputElement
    const file = fileInput.files?.[0]
    if (!file) return

    setUploading(true)
    setUploadMessage(null)
    try {
      await uploadMaterial(file, titleInput.value)
      setUploadMessage({ ok: true, text: '上传成功' })
      form.reset()
      await refreshList(query)
    } catch (err) {
      const message = err instanceof ApiError ? err.message : '上传失败'
      setUploadMessage({ ok: false, text: message })
    } finally {
      setUploading(false)
    }
  }

  return (
    <div className="materials-page">
      <header className="materials-header">
        <h1>{user?.class_name} 教学材料</h1>
        <div className="header-actions">
          <span className="who-am-i">
            {user?.username}（{user?.role === 'teacher' ? '教师' : '学生'}）
          </span>
          <ThemeToggle />
          <button type="button" onClick={logout}>
            登出
          </button>
        </div>
      </header>

      <input
        className="search-box"
        placeholder="按标题搜索本班材料"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
      />

      {loadError && <p className="error-text">{loadError}</p>}

      {user?.role === 'teacher' && (
        <form className="upload-form" onSubmit={handleUpload}>
          <input type="file" name="file" accept=".txt,.md" required />
          <input type="text" name="title" placeholder="标题（可选）" />
          <button type="submit" disabled={uploading}>
            {uploading ? '上传中…' : '上传材料'}
          </button>
          {uploadMessage && (
            <span className={uploadMessage.ok ? 'success-text' : 'error-text'}>{uploadMessage.text}</span>
          )}
        </form>
      )}

      <ul className="materials-list">
        {materials.map((m) => (
          <li key={m.id}>
            <button type="button" onClick={() => openDetail(m.id)}>
              {m.title}
            </button>
          </li>
        ))}
      </ul>

      {selected && (
        <div className="material-detail">
          <div className="material-detail-header">
            <h2>{selected.title}</h2>
            <div>
              <a href={fileDownloadUrl(selected.id)}>下载原文件</a>
              <button type="button" onClick={() => setSelected(null)}>
                关闭
              </button>
            </div>
          </div>
          <div className="markdown-body">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{selected.body}</ReactMarkdown>
          </div>
        </div>
      )}
    </div>
  )
}
