// All requests go through same-origin /api. The bearer token is the only
// credential: it is kept in sessionStorage under a fixed key and attached as an
// Authorization header. No session id, username or role is ever stored — the
// server's /api/me response is the single source of identity.

import { createSSEParser } from './sse.ts'

export interface Me {
  username: string
  role: 'teacher' | 'student'
  class_id: number
  class_name: string
}

export interface MaterialSummary {
  id: number
  title: string
  class_id: number
  created_at: string
}

export interface MaterialDetail extends MaterialSummary {
  body: string
}

export interface LoginResponse {
  token: string
  username: string
  role: 'teacher' | 'student'
}

export type SearchMode = 'hybrid' | 'keyword' | 'vector'

// Hit is one traceable result: the material it came from, the chunk position
// inside it and an excerpt read from MySQL. Search hits and ask citations share
// this shape.
export interface Hit {
  material_id: number
  material_title: string
  chunk_id: number
  chunk_index: number
  char_start: number
  char_end: number
  excerpt: string
  score: number
}

export interface SearchResponse {
  mode: SearchMode
  hits: Hit[]
  message?: string
}

export interface AskResponse {
  answer: string
  citations: Hit[]
}

// The only Web Storage key the app uses, and it holds nothing but the token.
const TOKEN_KEY = 'campusclaw_token'

export function getToken(): string | null {
  return sessionStorage.getItem(TOKEN_KEY)
}

export function saveToken(token: string): void {
  sessionStorage.setItem(TOKEN_KEY, token)
}

export function clearToken(): void {
  sessionStorage.removeItem(TOKEN_KEY)
}

export class ApiError extends Error {
  status: number
  // skills is set by a 409 from DELETE /api/mcp/servers/{id}: the skills that
  // still reference the server.
  skills?: string[]
  constructor(status: number, message: string, skills?: string[]) {
    super(message)
    this.status = status
    this.skills = skills
  }
}

let unauthorizedHandler: (() => void) | null = null
export function setUnauthorizedHandler(fn: (() => void) | null) {
  unauthorizedHandler = fn
}

// withAuth is false only for POST /api/login. Every other request carries the
// bearer header, and no request relies on cookies.
async function request<T>(path: string, options: RequestInit = {}, withAuth = true): Promise<T> {
  const headers = new Headers(options.headers)
  const token = getToken()
  if (withAuth && token) headers.set('Authorization', `Bearer ${token}`)

  const res = await fetch(path, { ...options, headers })

  if (res.status === 401) {
    clearToken()
    unauthorizedHandler?.()
    const body = await safeJson(res)
    throw new ApiError(401, body?.error ?? '未登录或会话已失效')
  }

  if (!res.ok) {
    const body = await safeJson(res)
    throw new ApiError(res.status, body?.error ?? `请求失败（${res.status}）`, body?.skills)
  }

  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

async function safeJson(res: Response): Promise<{ error?: string; skills?: string[] } | null> {
  try {
    return await res.json()
  } catch {
    return null
  }
}

// login opts out of the automatic bearer attach so it can decide the header
// itself: when a token is already stored, the login request carries it, and on
// success the server revokes that old token before issuing the new one — a
// rotated session never leaves two live tokens behind.
export function login(username: string, password: string) {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`

  return request<LoginResponse>(
    '/api/login',
    {
      method: 'POST',
      headers,
      body: JSON.stringify({ username, password }),
    },
    false,
  )
}

export async function logout(): Promise<{ ok: boolean }> {
  try {
    return await request<{ ok: boolean }>('/api/logout', { method: 'POST' })
  } finally {
    // The token is cleared whether or not the server could be reached: the
    // user asked to log out.
    clearToken()
  }
}

export function fetchMe() {
  return request<Me>('/api/me')
}

export function listMaterials(q: string) {
  const query = q ? `?q=${encodeURIComponent(q)}` : ''
  return request<MaterialSummary[]>(`/api/materials${query}`)
}

export function getMaterial(id: number) {
  return request<MaterialDetail>(`/api/materials/${id}`)
}

export async function uploadMaterial(file: File, title: string): Promise<{ id: number; title: string }> {
  const form = new FormData()
  form.append('file', file)
  if (title) form.append('title', title)
  return request<{ id: number; title: string }>('/api/materials', {
    method: 'POST',
    body: form,
  })
}

// searchKnowledge and askKnowledge send only what the endpoints accept: the
// class scope comes from the bearer token, never from the body.
export function searchKnowledge(query: string, mode: SearchMode, signal?: AbortSignal) {
  return request<SearchResponse>('/api/search', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ query, mode }),
    signal,
  })
}

export function askKnowledge(question: string, signal?: AbortSignal) {
  return request<AskResponse>('/api/ask', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ question }),
    signal,
  })
}

// failureText maps a failed request to the text a page displays, or null when
// nothing is shown: a 401 has already cleared the token and returned to the
// login page, and an aborted request belongs to a page that is going away. The
// 503 text is fixed so no dependency detail can surface on a page.
export function failureText(err: unknown): string | null {
  if (err instanceof DOMException && err.name === 'AbortError') return null
  if (err instanceof ApiError) {
    if (err.status === 401) return null
    if (err.status === 503) return '检索服务暂不可用'
    if (err.status === 400) return err.message
  }
  return '请求失败，请稍后重试'
}

// filenameFromDisposition reads the RFC 5987 `filename*` value first (UTF-8
// percent-encoded, so non-ASCII names survive) and falls back to the ASCII
// `filename=` parameter.
export function filenameFromDisposition(header: string | null): string | null {
  if (!header) return null

  const utf8 = /filename\*=UTF-8''([^;]+)/i.exec(header)
  if (utf8) {
    try {
      return decodeURIComponent(utf8[1].trim())
    } catch {
      // Malformed percent-encoding: fall through to the ASCII parameter.
    }
  }

  const plain = /filename="([^"]*)"/i.exec(header) ?? /filename=([^;]+)/i.exec(header)
  return plain ? plain[1].trim() : null
}

// downloadMaterialFile fetches the original file with the Authorization header
// and saves it as a Blob. A plain <a href> cannot be used: navigation does not
// carry the bearer header, so the request would be rejected with 401.
export async function downloadMaterialFile(id: number): Promise<void> {
  const headers = new Headers()
  const token = getToken()
  if (token) headers.set('Authorization', `Bearer ${token}`)

  const res = await fetch(`/api/materials/${id}/file`, { headers })

  if (res.status === 401) {
    clearToken()
    unauthorizedHandler?.()
    throw new ApiError(401, '未登录或会话已失效')
  }
  if (!res.ok) {
    const body = await safeJson(res)
    throw new ApiError(res.status, body?.error ?? `下载失败（${res.status}）`)
  }

  const blob = await res.blob()
  const filename = filenameFromDisposition(res.headers.get('Content-Disposition')) ?? `material-${id}`

  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

// --- Tutor: agents, skills, conversations and the streaming turn ---

// A teacher's agent list carries prompt, enabled flag and binding ids; a
// student's carries only id and name (server-side projection).
export interface Agent {
  id: number
  name: string
  prompt: string
  enabled: boolean
  skill_ids: number[]
  material_ids: number[]
}

export interface AgentInput {
  name: string
  prompt: string
  enabled: boolean
  skill_ids: number[]
  material_ids: number[]
}

export interface Skill {
  id: number
  name: string
  trigger_text: string
  subject_text: string
  tools: string[]
  description: string
  // Present only in a teacher's response.
  source_text: string
  enabled: boolean
}

export interface ConversationSummary {
  id: number
  agent_id: number
  title: string
  created_at: string
  updated_at: string
}

export interface ConversationMessage {
  id: number
  role: 'user' | 'assistant'
  content: string
  citations: Hit[] | null
  skill_name: string
  created_at: string
}

export interface ConversationDetail extends ConversationSummary {
  messages: ConversationMessage[]
}

export function listAgents() {
  return request<Agent[]>('/api/agents')
}

export function createAgent(input: AgentInput) {
  return request<{ id: number }>('/api/agents', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
}

export function updateAgent(id: number, input: AgentInput) {
  return request<{ id: number }>(`/api/agents/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
}

export function listSkills() {
  return request<Skill[]>('/api/skills')
}

// Skill writes submit the raw skill file text; the server derives every field
// from it.
export function createSkill(source: string) {
  return request<{ id: number }>('/api/skills', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ source }),
  })
}

export function updateSkill(id: number, source: string) {
  return request<{ id: number }>(`/api/skills/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ source }),
  })
}

export function deleteSkill(id: number) {
  return request<{ ok: boolean }>(`/api/skills/${id}`, { method: 'DELETE' })
}

export function listConversations() {
  return request<ConversationSummary[]>('/api/conversations')
}

export function createConversation(agentID: number) {
  return request<ConversationSummary>('/api/conversations', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ agent_id: agentID }),
  })
}

export function getConversation(id: number) {
  return request<ConversationDetail>(`/api/conversations/${id}`)
}

// --- MCP tool servers and the call audit ---

export type MCPServerStatus = 'disconnected' | 'connected' | 'error'

// MCPServer never carries the token: has_token is the only signal.
export interface MCPServer {
  id: number
  name: string
  base_url: string
  has_token: boolean
  status: MCPServerStatus
  tool_count: number
  last_error: string
  tools_refreshed_at: string | null
}

export interface MCPTool {
  name: string
  description: string
  // Raw JSON Schema; the page renders it as formatted plain text.
  input_schema: unknown
}

export interface MCPToolCall {
  id: number
  /** 合并后的 `工具名@服务器名`，与 meta.tool_calls 的写法一致。 */
  tool: string
  status: 'ok' | 'error' | 'denied'
  reason?: string
  username: string
  conversation_id: number
  round: number
  duration_ms: number
  created_at: string
}

// ToolCallSummary is the meta projection: status and identifiers only, never
// arguments or result text.
export interface ToolCallSummary {
  tool: string
  status: 'ok' | 'error' | 'denied'
  reason?: string
  round: number
  duration_ms: number
}

export function listMCPServers() {
  return request<MCPServer[]>('/api/mcp/servers')
}

export function createMCPServer(input: { name: string; base_url: string; token: string }) {
  return request<MCPServer>('/api/mcp/servers', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
}

export function deleteMCPServer(id: number) {
  return request<void>(`/api/mcp/servers/${id}`, { method: 'DELETE' })
}

export function connectMCPServer(id: number) {
  return request<MCPTool[]>(`/api/mcp/servers/${id}/connect`, { method: 'POST' })
}

export function listMCPServerTools(id: number) {
  return request<MCPTool[]>(`/api/mcp/servers/${id}/tools`)
}

export function disconnectMCPServer(id: number) {
  return request<void>(`/api/mcp/servers/${id}/disconnect`, { method: 'POST' })
}

export function listMCPCalls(limit = 50) {
  return request<MCPToolCall[]>(`/api/mcp/calls?limit=${limit}`)
}

export interface StreamMeta {
  status: string
  skill: string | null
  citations: Hit[]
  tool_calls?: ToolCallSummary[]
}

export interface StreamHandlers {
  onMeta: (meta: StreamMeta) => void
  onDelta: (text: string) => void
  onDone: (messageID: number) => void
  onError: (message: string) => void
}

// streamConversation asks one question and reads the SSE answer with fetch —
// the Authorization header cannot ride on the browser's native event-stream
// client. Anything that fails before the stream starts is a plain JSON error —
// 401 clears the token and returns to the login page, 503 shows the fixed
// service text, and 400/404/409 keep the server's own error text. Mid-stream
// failures arrive as an `error` event (generic text, no internal detail).
export async function streamConversation(
  id: number,
  question: string,
  handlers: StreamHandlers,
  signal?: AbortSignal,
): Promise<void> {
  const headers = new Headers({ 'Content-Type': 'application/json' })
  const token = getToken()
  if (token) headers.set('Authorization', `Bearer ${token}`)

  const res = await fetch(`/api/conversations/${id}/stream`, {
    method: 'POST',
    headers,
    body: JSON.stringify({ question }),
    signal,
  })

  const isEventStream = (res.headers.get('Content-Type') ?? '').includes('text/event-stream')
  if (!res.ok || !isEventStream) {
    if (res.status === 401) {
      clearToken()
      unauthorizedHandler?.()
      throw new ApiError(401, '未登录或会话已失效')
    }
    if (res.status === 503) throw new ApiError(503, '服务暂不可用')
    const body = await safeJson(res)
    throw new ApiError(res.status, body?.error ?? `请求失败（${res.status}）`)
  }

  const stream = res.body
  if (!stream) throw new ApiError(res.status, '请求失败，请稍后重试')
  const reader = stream.getReader()
  const parser = createSSEParser((event) => {
    if (event.event === 'meta') handlers.onMeta(JSON.parse(event.data) as StreamMeta)
    else if (event.event === 'delta') handlers.onDelta((JSON.parse(event.data) as { text: string }).text)
    else if (event.event === 'done') handlers.onDone((JSON.parse(event.data) as { message_id: number }).message_id)
    else if (event.event === 'error') handlers.onError((JSON.parse(event.data) as { message: string }).message)
  })

  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      parser.feed(value)
    }
    parser.end()
  } finally {
    reader.releaseLock()
  }
}

// errorText is the text a tutor page shows for a failed request: null when
// nothing is shown (a 401 has already returned to the login page, an abort
// belongs to a view that is going away), otherwise the message the call
// attached — the server's own error text, the fixed stream texts, or a
// generic fallback.
export function errorText(err: unknown): string | null {
  if (err instanceof DOMException && err.name === 'AbortError') return null
  if (err instanceof ApiError) return err.status === 401 ? null : err.message
  return '请求失败，请稍后重试'
}
