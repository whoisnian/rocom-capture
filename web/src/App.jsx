import React, { useEffect, useRef, useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import { getAccounts, getCurrentAccount, setCurrentAccount, getIcons } from './api'
import { AccountContext, IconsContext } from './context'
import { BattleProvider } from './battle'
import { useStoredFlag } from './hooks/useStoredState'
import { Dropdown } from './components/dropdown'
import { dropBoxFilter } from './pages/pet-list/filters'

const NAV = [
  { to: '/pets', label: '宠物列表', icon: '🐾' },
  { to: '/events', label: '捕获事件', icon: '🔔' },
  { to: '/eggs', label: '精灵蛋', icon: '🥚' },
  { to: '/map', label: '实时地图', icon: '🗺️' },
]

// uidOf 从账号键 "UID:<user_id>" 取出 user_id(用于展示 nickname(user_id))。
const uidOf = (acc) => (acc || '').replace(/^UID:/, '')

// acctLabel 账号在下拉里的显示。昵称与 UID 分成两个 span:窄屏只留昵称(UID 那截由
// .acct-uid 隐藏,见 shell.css)——顶栏就那么宽,昵称一长两截都塞进去只能省略号收场。
const acctLabel = (a, i, showAcct) => (showAcct
  ? <><span className="acct-name">{a.name}</span><span className="acct-uid"> (UID:{uidOf(a.account)})</span></>
  : `账号 ${i + 1}`)

// App 全局壳:顶栏导航 + 账号切换 + 底部 tab(移动),并分发账号/图标两个全局 Context。
export default function App() {
  const [accounts, setAccounts] = useState([])
  const [account, setAccount] = useState(getCurrentAccount())
  const [icons, setIcons] = useState({ stat: {} })
  // 账号昵称与 UID **默认不显示**:页面常被截图分享,昵称/UID 属于不该顺手带出去的信息。
  // 隐藏时下拉仍按顺序列出「账号 1/2/…」,照样能切换,只是认不出是谁。开关记在 localStorage。
  const [showAcct, setShowAcct] = useStoredFlag(localStorage, 'accountReveal', false)
  const location = useLocation()
  const headRef = useRef(null)
  // 双击当前激活的导航项:平滑滚动回页面顶部(非激活项照常跳转,不滚动)
  const onNavDoubleClick = (to) => () => {
    if (location.pathname === to) window.scrollTo({ top: 0, behavior: 'smooth' })
  }

  // 把顶栏实际高度发布成 --topbar-h:左侧筛选栏按它吸附(sticky top)。写死一个 64px 的话,
  // 与它的自然位置(顶栏高度 + 内容区 16px 内边距)对不上,差多少就要先跟着页面漂多少才吸住。
  useEffect(() => {
    const el = headRef.current
    if (!el) return
    const apply = () => document.documentElement.style.setProperty('--topbar-h', el.offsetHeight + 'px')
    apply()
    if (typeof ResizeObserver === 'undefined') return // 老浏览器:退回上面量的这一次
    const ro = new ResizeObserver(apply)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  // 全局固定图标只随游戏版本变,拉一次即可。
  useEffect(() => { getIcons().then((d) => setIcons(d || { stat: {} })).catch(() => {}) }, [])

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
    dropBoxFilter()
    setAccount(a)
  }

  const navLinks = (base) => NAV.map((n) => (
    <NavLink key={n.to} to={n.to} onDoubleClick={onNavDoubleClick(n.to)}
      className={({ isActive }) => base + (isActive ? ' active' : '')}>
      <span className={base === 'tab' ? 'tab-icon' : 'nav-icon'}>{n.icon}</span>
      <span className={base === 'tab' ? 'tab-label' : 'nav-label'}>{n.label}</span>
    </NavLink>
  ))

  return (
    <AccountContext.Provider value={account}>
      <BattleProvider account={account}>
      <IconsContext.Provider value={icons}>
      <div className="app">
        <header className="topbar" ref={headRef}>
          <div className="brand">洛克助手</div>
          <nav className="topnav">{navLinks('navlink')}</nav>
          {accounts.length > 0 && (
            <div className="account-box">
              <Dropdown
                className="account-select" title="切换账号(玩家)"
                opts={accounts.map((a, i) => [a.account, acctLabel(a, i, showAcct)])}
                value={account} onChange={switchAccount}
              />
              <button
                className="btn btn-icon" onClick={() => setShowAcct((v) => !v)}
                title={showAcct ? '隐藏账号昵称与 UID(截图前点一下)' : '显示账号昵称与 UID'}
                aria-label={showAcct ? '隐藏账号信息' : '显示账号信息'}
              >{showAcct ? '👁' : '🙈'}</button>
            </div>
          )}
        </header>

        <main className="content" key={account}>
          <Outlet />
        </main>

        <nav className="bottomnav">{navLinks('tab')}</nav>
      </div>
      </IconsContext.Provider>
      </BattleProvider>
    </AccountContext.Provider>
  )
}
