// All requests go through same-origin /api with credentials so the
// HttpOnly session cookie is sent automatically. No session id, role or
// token is ever read or stored here — identity always comes from the
// server's response to /api/me.

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

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const res = await fetch(path, { ...options, credentials: 'include' })

  if (res.status === 401) {
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
  return request<{ username: string; role: string }>('/api/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
}

export function logout() {
  return request<{ ok: boolean }>('/api/logout', { method: 'POST' })
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

export function fileDownloadUrl(id: number) {
  return `/api/materials/${id}/file`
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
