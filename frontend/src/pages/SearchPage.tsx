import { useEffect, useRef, useState } from 'react'
import { failureText, searchKnowledge, type Hit, type SearchMode } from '../api/client'

const MODES: { value: SearchMode; label: string }[] = [
  { value: 'hybrid', label: '混合' },
  { value: 'keyword', label: '关键词' },
  { value: 'vector', label: '向量' },
]

export default function SearchPage() {
  const [query, setQuery] = useState('')
  const [mode, setMode] = useState<SearchMode>('hybrid')
  const [loading, setLoading] = useState(false)
  const [hits, setHits] = useState<Hit[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const abortRef = useRef<AbortController | null>(null)

  // Leaving the page (tab switch, logout) aborts the in-flight search.
  useEffect(() => () => abortRef.current?.abort(), [])

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const q = query.trim()
    // The disabled button already blocks these; the guard keeps Enter-key
    // submits from slipping past a pending request.
    if (!q || loading) return

    const controller = new AbortController()
    abortRef.current = controller
    setLoading(true)
    setHits(null)
    setError(null)
    try {
      const res = await searchKnowledge(q, mode, controller.signal)
      setHits(res.hits)
    } catch (err) {
      const text = failureText(err)
      if (text !== null) setError(text)
    } finally {
      setLoading(false)
    }
  }

  return (
    <>
      <h1>知识检索</h1>

      <form className="search-form" onSubmit={handleSubmit}>
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          maxLength={500}
          placeholder="输入要检索的内容"
        />
        <select value={mode} onChange={(e) => setMode(e.target.value as SearchMode)}>
          {MODES.map((m) => (
            <option key={m.value} value={m.value}>
              {m.label}
            </option>
          ))}
        </select>
        <button type="submit" disabled={loading || query.trim() === ''}>
          {loading ? '正在检索…' : '检索'}
        </button>
      </form>

      {error && <p className="error-text">{error}</p>}

      {hits !== null && hits.length === 0 && <p>资料中未找到相关内容</p>}

      {hits !== null && hits.length > 0 && (
        <ol className="hits-list">
          {hits.map((hit) => (
            <li key={hit.chunk_id} className="hit-item">
              <div className="hit-header">
                <span className="hit-title">{hit.material_title}</span>
                <span className="hit-meta">切片序号 {hit.chunk_index}</span>
                <span className="hit-meta" title="左闭右开">
                  字符 {hit.char_start}–{hit.char_end}
                </span>
              </div>
              <p className="hit-excerpt">{hit.excerpt}</p>
            </li>
          ))}
        </ol>
      )}
    </>
  )
}
