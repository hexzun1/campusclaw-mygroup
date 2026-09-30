import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import {
  clearToken,
  fetchMe,
  getToken,
  logout as apiLogout,
  setUnauthorizedHandler,
  type Me,
} from '../api/client'

interface AuthState {
  user: Me | null
  loading: boolean
  refresh: () => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthState | null>(null)

// Identity always comes from the server. The only thing this provider stores is
// the bearer token, kept in sessionStorage by the api client; role, class and
// username are never persisted and are taken from /api/me.
export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<Me | null>(null)
  const [loading, setLoading] = useState(true)

  const refresh = useCallback(async () => {
    // Without a token there is nothing to confirm: go straight to the login
    // page instead of firing a request that must fail.
    if (!getToken()) {
      setUser(null)
      setLoading(false)
      return
    }
    try {
      setUser(await fetchMe())
    } catch {
      clearToken()
      setUser(null)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    // Any 401 from any call — the client has already dropped the token by then.
    setUnauthorizedHandler(() => setUser(null))
    refresh()
    return () => setUnauthorizedHandler(null)
  }, [refresh])

  const logout = useCallback(async () => {
    try {
      await apiLogout()
    } finally {
      setUser(null)
    }
  }, [])

  return <AuthContext.Provider value={{ user, loading, refresh, logout }}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
