import { Navigate, Outlet } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext'

// TeacherOnly guards the admin pages: a student deep-linking to their URLs
// lands back on the materials page. This is display-level only — the server
// still answers 403 to a student's management requests (spec: 学生看不到管理入口).
export default function TeacherOnly() {
  const { user } = useAuth()
  return user?.role === 'teacher' ? <Outlet /> : <Navigate to="/materials" replace />
}
