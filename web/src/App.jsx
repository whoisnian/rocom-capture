import React, { useEffect, useState } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import { getStats } from './api'

const NAV = [
  { to: '/pets', label: '宠物列表', icon: '🐾' },
  { to: '/events', label: '实时事件', icon: '🔔' },
  { to: '/debug', label: '调试', icon: '🐞' },
]

export default function App() {
  const [count, setCount] = useState(null)
  useEffect(() => {
    getStats().then((s) => setCount(s.petCount)).catch(() => {})
  }, [])

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">洛克助手 <span className="brand-sub">宠物统计</span></div>
        <nav className="topnav">
          {NAV.map((n) => (
            <NavLink key={n.to} to={n.to} className={({ isActive }) => 'navlink' + (isActive ? ' active' : '')}>
              <span className="nav-icon">{n.icon}</span>
              <span className="nav-label">{n.label}</span>
            </NavLink>
          ))}
        </nav>
        <div className="count">{count != null ? `共 ${count} 只` : ''}</div>
      </header>

      <main className="content">
        <Outlet />
      </main>

      <nav className="bottomnav">
        {NAV.map((n) => (
          <NavLink key={n.to} to={n.to} className={({ isActive }) => 'tab' + (isActive ? ' active' : '')}>
            <span className="tab-icon">{n.icon}</span>
            <span className="tab-label">{n.label}</span>
          </NavLink>
        ))}
      </nav>
    </div>
  )
}
