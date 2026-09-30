// All requests go through same-origin /api. The bearer token is the only
// credential: it is kept in sessionStorage under a fixed key and attached as an
// Authorization header. No session id, username or role is ever stored — the
// server's /api/me response is the single source of identity.

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
  constructor(status: number, message: string) {
    super(message)
    this.status = status
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
    throw new ApiError(res.status, body?.error ?? `请求失败（${res.status}）`)
  }

  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

async function safeJson(res: Response): Promise<{ error?: string } | null> {
  try {
    return await res.json()
  } catch {
    return null
  }
}

export function login(username: string, password: string) {
  return request<LoginResponse>(
    '/api/login',
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
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
