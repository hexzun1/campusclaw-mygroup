import { Navigate, Route, Routes } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth/AuthContext'
import LoginPage from './pages/LoginPage'
import MaterialsPage from './pages/MaterialsPage'

function Gate() {
  const { user, loading } = useAuth()

  if (loading) return <div className="loading-screen">加载中…</div>

  return (
    <Routes>
      <Route path="/login" element={user ? <Navigate to="/materials" replace /> : <LoginPage />} />
      <Route path="/materials" element={user ? <MaterialsPage /> : <Navigate to="/login" replace />} />
      <Route path="*" element={<Navigate to={user ? '/materials' : '/login'} replace />} />
    </Routes>
  )
}

function App() {
  return (
    <AuthProvider>
      <Gate />
    </AuthProvider>
  )
}

export default App
