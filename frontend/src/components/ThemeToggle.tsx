import { useEffect, useState } from 'react'

export default function ThemeToggle() {
  const [dark, setDark] = useState(false)

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', dark ? 'dark' : 'light')
  }, [dark])

  return (
    <button type="button" className="theme-toggle" onClick={() => setDark((d) => !d)}>
      {dark ? '☀️ 浅色' : '🌙 深色'}
    </button>
  )
}
