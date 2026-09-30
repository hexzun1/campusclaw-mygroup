import { NavLink, Outlet } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext'
import ThemeToggle from './ThemeToggle'

// AppLayout is the shell of every signed-in page: the main navigation and the
// account actions on top, the current page below. Access control stays in the
// route guard (App.tsx); the layout only renders for a signed-in user.
export default function AppLayout() {
  const { user, logout } = useAuth()

  return (
    <div className="app-layout">
      <header className="app-header">
        <nav className="app-nav" aria-label="主导航">
          <NavLink to="/materials">材料</NavLink>
          <NavLink to="/search">知识检索</NavLink>
          <NavLink to="/ask">知识问答</NavLink>
        </nav>
        <div className="header-actions">
          <span className="who-am-i">
            {user?.username}（{user?.role === 'teacher' ? '教师' : '学生'}）
          </span>
          <ThemeToggle />
          <button type="button" onClick={logout}>
            登出
          </button>
        </div>
      </header>
      <main className="app-main">
        <Outlet />
      </main>
    </div>
  )
}
