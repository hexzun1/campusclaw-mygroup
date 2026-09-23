import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { fetchMe, logout as apiLogout, setUnauthorizedHandler, type Me } from '../api/client'

interface AuthState {
  user: Me | null
  loading: boolean
  refresh: () => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthState | null>(null)

// Identity always comes from the server. This provider never reads or
// writes localStorage/sessionStorage for the session id, role or token —
// only /api/me decides who is logged in.
export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<Me | null>(null)
  const [loading, setLoading] = useState(true)

  const refresh = useCallback(async () => {
    try {
      const me = await fetchMe()
      setUser(me)
    } catch {
      setUser(null)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
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
