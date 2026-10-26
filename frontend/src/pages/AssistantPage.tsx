import { useEffect, useRef, useState } from 'react'
import {
  createConversation,
  errorText,
  getConversation,
  getMaterial,
  listAgents,
  listConversations,
  streamConversation,
  type Agent,
  type ConversationDetail,
  type ConversationMessage,
  type ConversationSummary,
  type Hit,
  type MaterialDetail,
  type ToolCallSummary,
} from '../api/client'
import { useAuth } from '../auth/AuthContext'
import MaterialDetailView from '../components/MaterialDetailView'

// A live turn is the session-local half of the conversation: the question the
// user just asked plus whatever the stream has produced for it. Persisted
// messages are re-read from the server; once a turn finishes it stays here
// until the conversation is re-opened.
interface LiveTurn {
  id: number
  question: string
  status: 'pending' | 'done' | 'error'
  answer: string
  citations: Hit[]
  skill: string | null
  toolCalls: ToolCallSummary[]
  error: string | null
}

export default function AssistantPage() {
  const { user } = useAuth()
  const [conversations, setConversations] = useState<ConversationSummary[]>([])
  const [agents, setAgents] = useState<Agent[]>([])
  const [activeId, setActiveId] = useState<number | null>(null)
  const [detail, setDetail] = useState<ConversationDetail | null>(null)
  const [turns, setTurns] = useState<LiveTurn[]>([])
  const [question, setQuestion] = useState('')
  const [createAgentID, setCreateAgentID] = useState<number | ''>('')
  const [pageError, setPageError] = useState<string | null>(null)
  const [createError, setCreateError] = useState<string | null>(null)
  const [material, setMaterial] = useState<MaterialDetail | null>(null)
  const nextTurnRef = useRef(1)
  const controllersRef = useRef(new Map<number, AbortController>())
  const bottomRef = useRef<HTMLDivElement | null>(null)
  const panelRef = useRef<HTMLDivElement | null>(null)

  // The dropdown lists enabled agents only. A student's list already contains
  // only enabled agents (and no enabled flag); a teacher's list filters here.
  const agentOptions = user?.role === 'teacher' ? agents.filter((a) => a.enabled) : agents

  function abortTurns() {
    for (const controller of controllersRef.current.values()) controller.abort()
    controllersRef.current.clear()
  }

  function updateTurn(turnID: number, fn: (turn: LiveTurn) => LiveTurn) {
    setTurns((prev) => prev.map((t) => (t.id === turnID ? fn(t) : t)))
  }

  // Initial load: the caller's own conversations (latest first) and the
  // agents available for new conversations. The most recent conversation is
  // opened right away.
  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const [convs, ags] = await Promise.all([listConversations(), listAgents()])
        if (cancelled) return
        setConversations(convs)
        setAgents(ags)
        if (convs.length > 0) selectConversation(convs[0].id)
      } catch (err) {
        if (cancelled) return
        const text = errorText(err)
        if (text !== null) setPageError(text)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  // Coming back later abandons whatever stream was running in this tab; the
  // server persists what it already pushed.
  useEffect(() => () => abortTurns(), [])

  // Switching conversations resets the live half — abandoning any running
  // stream — and loads the persisted one.
  function selectConversation(id: number) {
    abortTurns()
    setTurns([])
    setDetail(null)
    setMaterial(null)
    setPageError(null)
    setActiveId(id)
  }

  useEffect(() => {
    if (activeId === null) return

    let cancelled = false
    void (async () => {
      try {
        const conv = await getConversation(activeId)
        if (!cancelled) setDetail(conv)
      } catch (err) {
        if (cancelled) return
        const text = errorText(err)
        if (text !== null) setPageError(text)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [activeId])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' })
  }, [detail, turns])

  useEffect(() => {
    if (material) panelRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }, [material])

  async function refreshConversations() {
    try {
      setConversations(await listConversations())
    } catch {
      // The sidebar keeps its current content; the answer itself is unaffected.
    }
  }

  async function handleCreate() {
    if (createAgentID === '') return
    setCreateError(null)
    try {
      const conv = await createConversation(createAgentID)
      setConversations((prev) => [conv, ...prev])
      selectConversation(conv.id)
    } catch (err) {
      const text = errorText(err)
      if (text !== null) setCreateError(text)
    }
  }

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const q = question.trim()
    if (activeId === null || q.length < 2 || q.length > 500) return

    const turnID = nextTurnRef.current++
    const controller = new AbortController()
    controllersRef.current.set(turnID, controller)
    setTurns((prev) => [
      ...prev,
      { id: turnID, question: q, status: 'pending', answer: '', citations: [], skill: null, toolCalls: [], error: null },
    ])
    setQuestion('')

    try {
      await streamConversation(
        activeId,
        q,
        {
          onMeta: (meta) =>
            updateTurn(turnID, (t) => ({
              ...t,
              citations: meta.citations,
              skill: meta.skill,
              toolCalls: meta.tool_calls ?? [],
            })),
          onDelta: (text) => updateTurn(turnID, (t) => ({ ...t, answer: t.answer + text })),
          onDone: () => {
            updateTurn(turnID, (t) => ({ ...t, status: 'done' }))
            void refreshConversations()
          },
          onError: (message) => updateTurn(turnID, (t) => ({ ...t, status: 'error', error: message })),
        },
        controller.signal,
      )
    } catch (err) {
      const text = errorText(err)
      if (text !== null) updateTurn(turnID, (t) => ({ ...t, status: 'error', error: text }))
    } finally {
      controllersRef.current.delete(turnID)
    }
  }

  async function openMaterial(materialID: number) {
    try {
      setMaterial(await getMaterial(materialID))
    } catch (err) {
      const text = errorText(err)
      if (text !== null) setPageError(text)
    }
  }

  function citationList(citations: Hit[]) {
    return (
      <ol className="ask-citations">
        {citations.map((c, i) => (
          <li key={c.chunk_id}>
            <button type="button" className="citation-item citation-link" onClick={() => void openMaterial(c.material_id)}>
              <div className="citation-header">
                <span className="citation-index">[{i + 1}]</span>
                <span className="citation-title">{c.material_title}</span>
                <span className="citation-meta">切片序号 {c.chunk_index}</span>
                <span className="citation-meta" title="左闭右开">
                  字符 {c.char_start}–{c.char_end}
                </span>
              </div>
              <p className="citation-excerpt">{c.excerpt}</p>
            </button>
          </li>
        ))}
      </ol>
    )
  }

  function renderMessage(m: ConversationMessage) {
    if (m.role === 'user') {
      return (
        <div key={`m-${m.id}`} className="ask-turn">
          <p className="ask-question">{m.content}</p>
        </div>
      )
    }
    const citations = m.citations ?? []
    return (
      <div key={`m-${m.id}`} className="ask-turn">
        <section className="ask-answer" aria-label="助手回答">
          <p className="ask-answer-body">{m.content}</p>
          {m.skill_name !== '' && <p className="chat-skill">技能：{m.skill_name}</p>}
          {citations.length > 0 && citationList(citations)}
        </section>
      </div>
    )
  }

  // The tags come from meta only: this turn's tool references and statuses,
  // never arguments or result text. denied/error use their own styles.
  function toolCallTags(calls: ToolCallSummary[]) {
    const label: Record<ToolCallSummary['status'], string> = { ok: '成功', error: '失败', denied: '被拒绝' }
    return (
      <ul className="tool-calls" aria-label="工具调用">
        {calls.map((c, i) => (
          <li key={`${c.tool}-${i}`} className={`tool-call tool-call-${c.status}`}>
            {c.tool} · {label[c.status]}
            {c.reason ? `（${c.reason}）` : ''} · 第 {c.round} 轮
          </li>
        ))}
      </ul>
    )
  }

  function renderTurn(t: LiveTurn) {
    return (
      <div key={`t-${t.id}`} className="ask-turn">
        <p className="ask-question">{t.question}</p>
        {t.toolCalls.length > 0 && toolCallTags(t.toolCalls)}
        {t.answer === '' && t.status === 'pending' && <p className="ask-pending">正在生成回答…</p>}
        {t.answer !== '' && (
          <section className="ask-answer" aria-label="助手回答">
            <p className="ask-answer-body">{t.answer}</p>
            {t.status !== 'pending' && t.skill && <p className="chat-skill">技能：{t.skill}</p>}
            {t.status !== 'pending' && t.citations.length > 0 && citationList(t.citations)}
          </section>
        )}
        {t.status === 'error' && t.error && <p className="error-text">{t.error}</p>}
      </div>
    )
  }

  return (
    <>
      <h1>助手</h1>
      <div className="chat-layout">
        <aside className="chat-sidebar">
          <div className="chat-create">
            <select
              value={createAgentID}
              onChange={(e) => setCreateAgentID(e.target.value === '' ? '' : Number(e.target.value))}
              aria-label="选择智能体"
            >
              <option value="">选择智能体</option>
              {agentOptions.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
            <button type="button" onClick={() => void handleCreate()} disabled={createAgentID === ''}>
              新建会话
            </button>
          </div>
          {createError && <p className="error-text">{createError}</p>}

          <ul className="chat-conversations">
            {conversations.map((c) => (
              <li key={c.id}>
                <button
                  type="button"
                  className={c.id === activeId ? 'chat-conversation active' : 'chat-conversation'}
                  aria-current={c.id === activeId ? 'true' : undefined}
                  onClick={() => selectConversation(c.id)}
                >
                  {c.title}
                </button>
              </li>
            ))}
          </ul>
          {conversations.length === 0 && <p className="chat-empty">还没有会话</p>}
        </aside>

        <section className="chat-main">
          {pageError && <p className="error-text">{pageError}</p>}
          {activeId === null && <p className="chat-empty">在左侧选择或新建一个会话</p>}
          {activeId !== null && detail === null && <p className="chat-empty">正在加载会话…</p>}
          {detail && (
            <div className="ask-turns">
              {detail.messages.map(renderMessage)}
              {turns.map(renderTurn)}
              <div ref={bottomRef} />
            </div>
          )}

          <form className="ask-form" onSubmit={handleSubmit}>
            <input
              value={question}
              onChange={(e) => setQuestion(e.target.value)}
              maxLength={500}
              placeholder="输入你的问题（2–500 字）"
              disabled={activeId === null}
            />
            <button type="submit" disabled={activeId === null || question.trim().length < 2}>
              提问
            </button>
          </form>

          <div ref={panelRef}>
            {material && <MaterialDetailView material={material} onClose={() => setMaterial(null)} />}
          </div>
        </section>
      </div>
    </>
  )
}
