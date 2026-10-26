import { useEffect, useState } from 'react'
import {
  connectMCPServer,
  createMCPServer,
  deleteMCPServer,
  disconnectMCPServer,
  errorText,
  listMCPCalls,
  listMCPServers,
  listMCPServerTools,
  ApiError,
  type MCPTool,
  type MCPToolCall,
  type MCPServer,
  type MCPServerStatus,
} from '../api/client'

const statusText: Record<MCPServerStatus, string> = {
  connected: '已连接',
  disconnected: '未连接',
  error: '连接失败',
}

function formatSchema(schema: unknown): string {
  if (schema === null || schema === undefined) return '（无参数结构）'
  try {
    return JSON.stringify(schema, null, 2)
  } catch {
    return String(schema)
  }
}

export default function MCPAdminPage() {
  const [servers, setServers] = useState<MCPServer[]>([])
  const [calls, setCalls] = useState<MCPToolCall[]>([])
  const [pageError, setPageError] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const [name, setName] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [token, setToken] = useState('')
  const [formError, setFormError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  const [busy, setBusy] = useState<string | null>(null)
  const [confirmingID, setConfirmingID] = useState<number | null>(null)
  const [toolsPanel, setToolsPanel] = useState<{ server: MCPServer; tools: MCPTool[] } | null>(null)

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const [list, audit] = await Promise.all([listMCPServers(), listMCPCalls()])
        if (cancelled) return
        setServers(list)
        setCalls(audit)
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

  async function refresh() {
    const [list, audit] = await Promise.all([listMCPServers(), listMCPCalls()])
    setServers(list)
    setCalls(audit)
  }

  async function handleCreate(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    setSaving(true)
    setFormError(null)
    setActionError(null)
    setNotice(null)
    try {
      await createMCPServer({ name, base_url: baseURL, token })
      await refresh()
      setName('')
      setBaseURL('')
      setNotice('已登记')
    } catch (err) {
      const text = errorText(err)
      if (text !== null) setFormError(text)
    } finally {
      // The token is write-only for the page too: never keep it after submit.
      setToken('')
      setSaving(false)
    }
  }

  async function runAction(key: string, action: () => Promise<void>) {
    setBusy(key)
    setActionError(null)
    setNotice(null)
    let failure: string | null = null
    try {
      await action()
    } catch (err) {
      const text = errorText(err)
      if (text !== null) failure = text
    }
    // A failed action still changes the server: a rejected connect sets
    // status=error and empties the tool cache, so the list must be re-read
    // either way (spec: 每一步后列表 MUST 反映最新状态).
    try {
      await refresh()
    } catch (err) {
      const text = errorText(err)
      if (text !== null) failure ??= text
    }
    if (failure !== null) setActionError(failure)
    setBusy(null)
  }

  function handleConnect(server: MCPServer) {
    void runAction(`connect-${server.id}`, async () => {
      const tools = await connectMCPServer(server.id)
      setToolsPanel({ server, tools })
      setNotice(`已连接 ${server.name}，发现 ${tools.length} 个工具`)
    })
  }

  function handleDisconnect(server: MCPServer) {
    void runAction(`disconnect-${server.id}`, async () => {
      await disconnectMCPServer(server.id)
      if (toolsPanel?.server.id === server.id) setToolsPanel(null)
      setNotice(`已断开 ${server.name}`)
    })
  }

  async function handleDelete(server: MCPServer) {
    setBusy(`delete-${server.id}`)
    setActionError(null)
    setNotice(null)
    try {
      await deleteMCPServer(server.id)
      if (toolsPanel?.server.id === server.id) setToolsPanel(null)
      await refresh()
      setNotice(`已删除 ${server.name}`)
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        const skills = err.skills?.join('、') ?? ''
        setActionError(skills ? `${err.message}：${skills}` : err.message)
      } else {
        const text = errorText(err)
        if (text !== null) setActionError(text)
      }
    } finally {
      setConfirmingID(null)
      setBusy(null)
    }
  }

  function handleViewTools(server: MCPServer) {
    void runAction(`tools-${server.id}`, async () => {
      const tools = await listMCPServerTools(server.id)
      setToolsPanel({ server, tools })
    })
  }

  async function refreshCalls() {
    setActionError(null)
    try {
      setCalls(await listMCPCalls())
    } catch (err) {
      const text = errorText(err)
      if (text !== null) setActionError(text)
    }
  }

  return (
    <>
      <h1>工具服务器</h1>
      {pageError && <p className="error-text">{pageError}</p>}
      {actionError && <p className="error-text">{actionError}</p>}
      {notice && <p className="admin-hint">{notice}</p>}

      <form className="admin-form" onSubmit={handleCreate}>
        <h2>登记服务器</h2>
        <label>
          名称
          <input value={name} onChange={(e) => setName(e.target.value)} maxLength={32} placeholder="campus-tools" />
        </label>
        <label>
          地址
          <input
            value={baseURL}
            onChange={(e) => setBaseURL(e.target.value)}
            maxLength={200}
            placeholder="http://mcp-demo:8090"
          />
        </label>
        <label>
          令牌
          <input
            type="password"
            autoComplete="off"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            maxLength={512}
          />
        </label>
        <p className="admin-hint">名称只含小写字母、数字与连字符；地址不要以 /mcp 结尾；令牌只写不读。</p>
        {formError && <p className="error-text">{formError}</p>}
        <div className="admin-actions">
          <button type="submit" disabled={saving}>
            {saving ? '提交中…' : '登记'}
          </button>
        </div>
      </form>

      <ul className="admin-list">
        {servers.map((s) => (
          <li key={s.id} className="admin-item">
            <div className="admin-item-main">
              <span className="admin-item-name">{s.name}</span>
              <span className={`server-status server-status-${s.status}`}>{statusText[s.status]}</span>
              <span className="admin-item-meta">{s.base_url}</span>
              <span className="admin-item-meta">令牌 {s.has_token ? '已配置' : '未配置'}</span>
              <span className="admin-item-meta">工具 {s.tool_count} 个</span>
              {s.last_error !== '' && <span className="admin-item-meta">最近错误：{s.last_error}</span>}
            </div>
            <div className="admin-actions">
              <button type="button" onClick={() => handleConnect(s)} disabled={busy !== null}>
                {busy === `connect-${s.id}` ? '连接中…' : '连接'}
              </button>
              <button type="button" onClick={() => void handleViewTools(s)} disabled={busy !== null}>
                {busy === `tools-${s.id}` ? '读取中…' : '查看工具'}
              </button>
              <button type="button" onClick={() => handleDisconnect(s)} disabled={busy !== null}>
                断开
              </button>
              {confirmingID === s.id ? (
                <>
                  <button type="button" onClick={() => void handleDelete(s)} disabled={busy !== null}>
                    确认删除
                  </button>
                  <button type="button" onClick={() => setConfirmingID(null)}>
                    取消
                  </button>
                </>
              ) : (
                <button type="button" onClick={() => setConfirmingID(s.id)} disabled={busy !== null}>
                  删除
                </button>
              )}
            </div>
          </li>
        ))}
      </ul>
      {servers.length === 0 && <p className="chat-empty">本班还没有登记工具服务器</p>}

      {toolsPanel && (
        <section className="admin-form" aria-label="工具清单">
          <h2>{toolsPanel.server.name} 的工具</h2>
          {toolsPanel.tools.length === 0 && <p className="chat-empty">这台服务器还没有可用工具，请先连接。</p>}
          {toolsPanel.tools.map((t) => (
            <div key={t.name} className="mcp-tool">
              <p className="admin-item-name">{t.name}</p>
              <p className="admin-hint">{t.description || '（无说明）'}</p>
              <pre className="tool-schema">{formatSchema(t.input_schema)}</pre>
            </div>
          ))}
          <div className="admin-actions">
            <button type="button" onClick={() => setToolsPanel(null)}>
              关闭
            </button>
          </div>
        </section>
      )}

      <h2>本班调用审计</h2>
      <div className="admin-actions">
        <button type="button" onClick={() => void refreshCalls()}>
          刷新
        </button>
      </div>
      <div className="audit-wrap">
        <table className="audit-table">
          <thead>
            <tr>
              <th>时间</th>
              <th>工具</th>
              <th>状态</th>
              <th>原因</th>
              <th>用户</th>
              <th>会话</th>
              <th>轮次</th>
              <th>耗时</th>
            </tr>
          </thead>
          <tbody>
            {calls.map((c) => (
              <tr key={c.id}>
                <td>{new Date(c.created_at).toLocaleString()}</td>
                <td>{c.tool}</td>
                <td>
                  <span className={`tool-call tool-call-${c.status}`}>{c.status}</span>
                </td>
                <td>{c.reason ?? ''}</td>
                <td>{c.username}</td>
                <td>{c.conversation_id}</td>
                <td>{c.round}</td>
                <td>{c.duration_ms} ms</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {calls.length === 0 && <p className="chat-empty">还没有调用记录</p>}
    </>
  )
}
