import { Navigate, Route, Routes } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth/AuthContext'
import AppLayout from './components/AppLayout'
import AskPage from './pages/AskPage'
import LoginPage from './pages/LoginPage'
import MaterialsPage from './pages/MaterialsPage'
import SearchPage from './pages/SearchPage'

function Gate() {
  const { user, loading } = useAuth()

  if (loading) return <div className="loading-screen">加载中…</div>

  return (
    <Routes>
      <Route path="/login" element={user ? <Navigate to="/materials" replace /> : <LoginPage />} />
      <Route element={user ? <AppLayout /> : <Navigate to="/login" replace />}>
        <Route path="/materials" element={<MaterialsPage />} />
        <Route path="/search" element={<SearchPage />} />
        <Route path="/ask" element={<AskPage />} />
      </Route>
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
