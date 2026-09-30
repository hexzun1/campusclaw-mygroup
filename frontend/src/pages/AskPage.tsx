import { useEffect, useRef, useState } from 'react'
import { askKnowledge, failureText, type Hit } from '../api/client'

interface Turn {
  id: number
  question: string
  status: 'pending' | 'done' | 'error'
  answer?: string
  citations?: Hit[]
  error?: string
}

export default function AskPage() {
  const [question, setQuestion] = useState('')
  const [turns, setTurns] = useState<Turn[]>([])
  const nextIdRef = useRef(1)
  const abortRef = useRef<AbortController | null>(null)
  const bottomRef = useRef<HTMLDivElement | null>(null)
  const pending = turns.some((t) => t.status === 'pending')

  // Leaving the page (tab switch, logout) aborts the in-flight ask.
  useEffect(() => () => abortRef.current?.abort(), [])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' })
  }, [turns.length])

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const q = question.trim()
    // The disabled input/button already block these; the guard keeps Enter-key
    // submits from slipping past a pending request.
    if (!q || pending) return

    const controller = new AbortController()
    abortRef.current = controller
    const id = nextIdRef.current++

    setTurns((prev) => [...prev, { id, question: q, status: 'pending' }])
    setQuestion('')
    try {
      const res = await askKnowledge(q, controller.signal)
      setTurns((prev) =>
        prev.map((t) => (t.id === id ? { ...t, status: 'done', answer: res.answer, citations: res.citations } : t)),
      )
    } catch (err) {
      const text = failureText(err)
      if (text !== null) {
        setTurns((prev) => prev.map((t) => (t.id === id ? { ...t, status: 'error', error: text } : t)))
      }
    }
  }

  return (
    <>
      <h1>知识问答</h1>
      <p className="ask-hint">每个问题先在本班做混合检索，取前 4 条切片再生成简短回答，并标出 [1]、[2]；没有命中时不调用模型</p>

      <div className="ask-turns">
        {turns.map((turn) => (
          <div key={turn.id} className="ask-turn">
            <p className="ask-question">{turn.question}</p>
            {turn.status === 'pending' && <p className="ask-pending">正在生成回答…</p>}
            {turn.status === 'error' && <p className="error-text">{turn.error}</p>}
            {turn.status === 'done' && turn.citations && turn.citations.length > 0 && (
              <section className="ask-answer" aria-label="依据回答">
                <h2>依据回答</h2>
                <p className="ask-answer-body">{turn.answer}</p>
                <ol className="ask-citations">
                  {turn.citations.map((c, i) => (
                    <li key={c.chunk_id} className="citation-item">
                      <div className="citation-header">
                        <span className="citation-index">[{i + 1}]</span>
                        <span className="citation-title">{c.material_title}</span>
                        <span className="citation-meta">切片序号 {c.chunk_index}</span>
                        <span className="citation-meta" title="左闭右开">
                          字符 {c.char_start}–{c.char_end}
                        </span>
                      </div>
                      <p className="citation-excerpt">{c.excerpt}</p>
                    </li>
                  ))}
                </ol>
              </section>
            )}
            {turn.status === 'done' && (!turn.citations || turn.citations.length === 0) && (
              <p>资料中未找到相关内容</p>
            )}
          </div>
        ))}
        <div ref={bottomRef} />
      </div>

      <form className="ask-form" onSubmit={handleSubmit}>
        <input
          value={question}
          onChange={(e) => setQuestion(e.target.value)}
          maxLength={1000}
          placeholder="输入你的问题"
          disabled={pending}
        />
        <button type="submit" disabled={pending || question.trim() === ''}>
          {pending ? '正在生成回答…' : '提问'}
        </button>
      </form>
    </>
  )
}
