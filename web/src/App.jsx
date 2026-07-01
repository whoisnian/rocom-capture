import React, { useEffect, useState, createContext } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import { getAccounts, getCurrentAccount, setCurrentAccount } from './api'

const NAV = [
  { to: '/pets', label: '宠物列表', icon: '🐾' },
  { to: '/events', label: '实时事件', icon: '🔔' },
  { to: '/debug', label: '调试', icon: '🐞' },
]

// AccountContext 提供当前选中账号(玩家 user_id key),供各页对 SSE 按账号过滤。
export const AccountContext = createContext('')

// uidOf 从账号键 "role:<user_id>" 取出 user_id(用于展示 nickname(user_id))。
const uidOf = (acc) => (acc || '').replace(/^role:/, '')

export default function App() {
  const [accounts, setAccounts] = useState([])
  const [account, setAccount] = useState(getCurrentAccount())

  // 拉账号列表;当前无选中(或选中的已不存在)时默认选最近活跃的第一个。
  useEffect(() => {
    getAccounts().then((list) => {
      list = list || []
      setAccounts(list)
      const cur = getCurrentAccount()
      if ((!cur || !list.some((a) => a.account === cur)) && list.length) {
        setCurrentAccount(list[0].account)
        setAccount(list[0].account)
      }
    }).catch(() => {})
  }, [])

  // 切换账号:更新 api.js 当前账号、清掉与旧账号绑定的盒子筛选,再切 state
  // (下方 <main key={account}> 据此重挂各页,让其以新账号重新拉数据)。
  const switchAccount = (a) => {
    if (!a || a === account) return
    setCurrentAccount(a)
    try {
      const f = JSON.parse(sessionStorage.getItem('petListFilter'))
      if (f && f.box) { delete f.box; sessionStorage.setItem('petListFilter', JSON.stringify(f)) }
    } catch { /* ignore */ }
    setAccount(a)
  }

  return (
    <AccountContext.Provider value={account}>
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
          {accounts.length > 0 && (
            <select
              className="select account-select"
              value={account} onChange={(e) => switchAccount(e.target.value)}
              title="切换账号(玩家)"
            >
              {accounts.map((a) => (
                <option key={a.account} value={a.account}>{a.name} ({uidOf(a.account)})</option>
              ))}
            </select>
          )}
        </header>

        <main className="content" key={account}>
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
    </AccountContext.Provider>
  )
}
