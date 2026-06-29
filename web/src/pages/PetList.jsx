import React, { useState, useEffect, useCallback, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { getPets, getFilterOptions, subscribe, ALL_TYPES } from '../api'
import { Types, Six, Marks, Avatar, boxLabel, teamLabel, fmtTime } from '../components/bits'

// 热门性格(筛选用)及其影响。其余归入"其他"。
const HOT_NATURES = [
  ['开朗', '速度↑魔攻↓'],
  ['胆小', '速度↑物攻↓'],
  ['固执', '物攻↑魔攻↓'],
  ['聪明', '魔攻↑物攻↓'],
  ['平和', '生命↑魔攻↓'],
  ['踏实', '生命↑速度↓'],
  ['沉默', '生命↑物攻↓'],
  ['急躁', '速度↑物防↓'],
]
const HOT_NAMES = HOT_NATURES.map((n) => n[0])

const SORTS = [
  { key: 'gid', label: '编号' },
  { key: 'level', label: '等级' },
  { key: 'weight', label: '体重' },
  { key: 'height', label: '身高' },
  { key: 'voice', label: '声音' },
  { key: 'catchTime', label: '捕捉时间' },
]

// 列表状态(筛选/排序/分页/滚动)用 sessionStorage 持久化,从详情返回时还原。
const DEFAULT_FILTER = { page: 1, pageSize: 10, sort: 'gid', order: 'asc' }
function loadFilter() {
  try {
    const s = JSON.parse(sessionStorage.getItem('petListFilter'))
    if (s && typeof s === 'object') return s
  } catch { /* ignore */ }
  return DEFAULT_FILTER
}

export default function PetList() {
  const nav = useNavigate()
  const [filter, setFilter] = useState(loadFilter)
  const [data, setData] = useState({ total: 0, pets: [] })
  const [options, setOptions] = useState({})
  const [collapsed, setCollapsed] = useState(() => sessionStorage.getItem('petListCollapsed') !== '0')
  const [selected, setSelected] = useState(null) // 单击选中的 gid
  const [menu, setMenu] = useState(null)          // 右键/长按菜单 {gid,x,y}
  const reloadRef = useRef(null)
  const restoredRef = useRef(false)
  const lpRef = useRef(null)        // 长按定时器
  const lpFiredRef = useRef(false)  // 本次触摸是否已触发长按
  const menuAtRef = useRef(0)       // 菜单打开时刻(用于忽略紧随的合成 click)

  const load = useCallback(() => { getPets(filter).then(setData).catch(() => {}) }, [filter])
  useEffect(() => { load() }, [load])
  useEffect(() => { getFilterOptions().then(setOptions).catch(() => {}) }, [])

  // 持久化筛选状态与筛选栏折叠态
  useEffect(() => { sessionStorage.setItem('petListFilter', JSON.stringify(filter)) }, [filter])
  useEffect(() => { sessionStorage.setItem('petListCollapsed', collapsed ? '1' : '0') }, [collapsed])

  // 首次数据到达后还原滚动位置(从详情返回)
  useEffect(() => {
    if (restoredRef.current || data.pets.length === 0) return
    const y = parseInt(sessionStorage.getItem('petListScroll') || '0', 10)
    if (y > 0) window.scrollTo(0, y)
    restoredRef.current = true
  }, [data])

  // 实时：收到宠物更新时防抖重载当前页
  useEffect(() => {
    return subscribe((m) => {
      if (m.type !== 'pet') return
      clearTimeout(reloadRef.current)
      reloadRef.current = setTimeout(() => reloadRef.current && load(), 600)
    })
  }, [load])

  const set = (patch) => setFilter((f) => ({ ...f, ...patch, page: patch.page || 1 }))
  const toggleType = (t) =>
    setFilter((f) => {
      const s = new Set(f.types || [])
      s.has(t) ? s.delete(t) : s.add(t)
      return { ...f, types: [...s], page: 1 }
    })
  const sortBy = (key) =>
    setFilter((f) => ({ ...f, sort: key, order: f.sort === key && f.order === 'asc' ? 'desc' : 'asc', page: 1 }))
  // 进详情前记录滚动位置
  const goDetail = (gid) => { sessionStorage.setItem('petListScroll', String(window.scrollY)); nav('/pets/' + gid) }
  // 重置:清空所有过滤条件,保留排序与每页档位
  const reset = () => setFilter((f) => ({ page: 1, pageSize: f.pageSize, sort: f.sort, order: f.order }))

  // 右键/长按菜单:选中并在 (x,y) 弹出(限制不溢出视口)
  const openMenu = (gid, x, y) => {
    setSelected(gid)
    menuAtRef.current = Date.now()
    setMenu({ gid, x: Math.min(x, window.innerWidth - 140), y: Math.min(y, window.innerHeight - 60) })
  }
  // 菜单打开后:点击空白/滚动/Esc 关闭(忽略打开瞬间紧随的合成 click)
  useEffect(() => {
    if (!menu) return
    const close = (e) => {
      if (e && e.target && e.target.closest && e.target.closest('.ctx-menu')) return
      if (Date.now() - menuAtRef.current < 350) return
      setMenu(null)
    }
    const onKey = (e) => { if (e.key === 'Escape') setMenu(null) }
    window.addEventListener('click', close)
    window.addEventListener('scroll', close, true)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('click', close)
      window.removeEventListener('scroll', close, true)
      window.removeEventListener('keydown', onKey)
    }
  }, [menu])

  // 列表项交互:单击选中、右键(桌面)/长按(移动)弹菜单
  const itemProps = (gid) => ({
    onClick: () => { if (lpFiredRef.current) { lpFiredRef.current = false; return } setSelected(gid) },
    onContextMenu: (e) => { e.preventDefault(); openMenu(gid, e.clientX, e.clientY) },
    onTouchStart: (e) => {
      lpFiredRef.current = false
      const t = e.touches[0]
      lpRef.current = setTimeout(() => { lpFiredRef.current = true; openMenu(gid, t.clientX, t.clientY) }, 450)
    },
    onTouchMove: () => clearTimeout(lpRef.current),
    onTouchEnd: () => clearTimeout(lpRef.current),
  })

  const pages = Math.max(1, Math.ceil(data.total / filter.pageSize))
  const arrow = (k) => (filter.sort === k ? (filter.order === 'asc' ? ' ▲' : ' ▼') : '')
  const boxTag = (p) => (p.box ? ` · 📦${boxLabel(p.box)}` : p.team ? ` · 🌍大世界${teamLabel(p.team)}` : '')

  return (
    <div className="list-layout">
      <aside className={'filters' + (collapsed ? ' collapsed' : '')}>
        <div className="filter-group">
          <button className="btn" onClick={reset}>重置筛选</button>
        </div>
        <div className="filter-group">
          <label>系别</label>
          <div className="chips">
            {ALL_TYPES.map((t) => (
              <span key={t} className={'chip' + ((filter.types || []).includes(t) ? ' on' : '')} onClick={() => toggleType(t)}>{t}</span>
            ))}
          </div>
        </div>
        <div className="filter-group">
          <label>等级</label>
          <div className="range">
            <input className="input" type="number" placeholder="最小" value={filter.levelMin || ''} onChange={(e) => set({ levelMin: e.target.value })} />
            <span className="muted">~</span>
            <input className="input" type="number" placeholder="最大" value={filter.levelMax || ''} onChange={(e) => set({ levelMax: e.target.value })} />
          </div>
        </div>
        <div className="filter-group">
          <label>性格</label>
          <select
            className="select"
            value={filter.natureExclude ? '__other__' : filter.nature || ''}
            onChange={(e) => {
              const v = e.target.value
              if (v === '__other__') set({ nature: '', natureExclude: HOT_NAMES.join(',') })
              else set({ nature: v, natureExclude: '' })
            }}
          >
            <option value="">全部</option>
            {HOT_NATURES.map(([n, eff]) => (
              <option key={n} value={n}>{n}（{eff}）</option>
            ))}
            <option value="__other__">其他</option>
          </select>
        </div>
        <Select label="天分" opts={options.talentRank} value={filter.talentRank} onChange={(v) => set({ talentRank: v })} />
        <Select label="特长" opts={options.speciality} value={filter.speciality} onChange={(v) => set({ speciality: v })} />
        <Select label="奖牌" opts={options.medal} value={filter.medal} onChange={(v) => set({ medal: v })} />
        <Select label="宠物盒" opts={options.box} value={filter.box} onChange={(v) => set({ box: v })} />
        <div className="filter-group">
          <label>性别</label>
          <select className="select" value={filter.gender || ''} onChange={(e) => set({ gender: e.target.value })}>
            <option value="">全部</option><option value="♂">♂</option><option value="♀">♀</option>
          </select>
        </div>
        <div className="filter-group">
          <label>异色</label>
          <select className="select" value={filter.shiny || ''} onChange={(e) => set({ shiny: e.target.value })}>
            <option value="">全部</option><option value="1">仅异色</option><option value="0">非异色</option>
          </select>
        </div>
        <div className="filter-group">
          <label>炫彩</label>
          <select className="select" value={filter.colorful || ''} onChange={(e) => set({ colorful: e.target.value })}>
            <option value="">全部</option><option value="1">仅炫彩</option><option value="0">非炫彩</option>
          </select>
        </div>
      </aside>

      <section>
        <div className="toolbar">
          <button className="btn filter-toggle" onClick={() => setCollapsed((c) => !c)}>筛选</button>
          <input className="input" placeholder="搜索昵称 / 种类" value={filter.search || ''} onChange={(e) => set({ search: e.target.value })} />
          <select className="select" style={{ maxWidth: 130 }} value={filter.sort} onChange={(e) => set({ sort: e.target.value })}>
            {SORTS.map((s) => <option key={s.key} value={s.key}>{s.label}</option>)}
          </select>
          <button className="btn" onClick={() => set({ order: filter.order === 'asc' ? 'desc' : 'asc' })}>{filter.order === 'asc' ? '升序' : '降序'}</button>
          <div className="spacer" />
          <span className="muted">共 {data.total} 只</span>
        </div>

        {/* 桌面表格 */}
        <div className="table-wrap">
          <table className="pets">
            <thead>
              <tr>
                <th onClick={() => sortBy('gid')}>宠物{arrow('gid')}</th>
                <th>系别</th><th>性格</th><th>特长</th><th>佩戴奖牌</th>
                <th onClick={() => sortBy('voice')}>声音{arrow('voice')}</th>
                <th onClick={() => sortBy('weight')}>体重{arrow('weight')}</th>
                <th onClick={() => sortBy('height')}>身高{arrow('height')}</th>
                <th>六维</th>
                <th onClick={() => sortBy('catchTime')}>捕捉时间{arrow('catchTime')}</th>
              </tr>
            </thead>
            <tbody>
              {data.pets.map((p) => (
                <tr key={p.gid} className={p.gid === selected ? 'selected' : ''} {...itemProps(p.gid)}>
                  <td>
                    <div className="pet-cell">
                      <Avatar p={p} />
                      <div>
                        <div className="pet-name">{p.name || p.species} {p.gender} <Marks p={p} /></div>
                        <div className="pet-sub">{p.species} · Lv.{p.level}{boxTag(p)}</div>
                      </div>
                    </div>
                  </td>
                  <td><Types types={p.types} /></td>
                  <td>{p.nature || '-'}</td>
                  <td>{p.speciality || '无'}</td>
                  <td>{p.medal || '-'}</td>
                  <td>{p.voice}</td>
                  <td>{p.weightKg} kg</td>
                  <td>{p.heightM} m</td>
                  <td><Six p={p} /></td>
                  <td className="muted">{fmtTime(p.catchTime)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        {/* 移动卡片 */}
        <div className="cards">
          {data.pets.map((p) => (
            <div className={'card' + (p.gid === selected ? ' selected' : '')} key={p.gid} {...itemProps(p.gid)}>
              <div className="card-head">
                <Avatar p={p} />
                <div style={{ flex: 1 }}>
                  <div className="pet-name">{p.name || p.species} {p.gender} <Marks p={p} /></div>
                  <div className="pet-sub">{p.species} · Lv.{p.level}{boxTag(p)}</div>
                </div>
                <Types types={p.types} />
              </div>
              <div className="card-grid">
                <div>性格：{p.nature || '-'}</div>
                <div>特长：{p.speciality || '无'}</div>
                <div>奖牌：{p.medal || '-'}</div>
                <div>体重：{p.weightKg}kg</div>
                <div>身高：{p.heightM}m</div>
                <div>声音：{p.voice}</div>
              </div>
              <Six p={p} />
            </div>
          ))}
        </div>

        {data.pets.length === 0 && <div className="empty">没有匹配的宠物</div>}

        <div className="pager">
          <button className="btn" disabled={filter.page <= 1} onClick={() => set({ page: filter.page - 1 })}>上一页</button>
          <span className="muted">{filter.page} / {pages}</span>
          <button className="btn" disabled={filter.page >= pages} onClick={() => set({ page: filter.page + 1 })}>下一页</button>
          <select className="select" style={{ width: 110 }} value={filter.pageSize} onChange={(e) => set({ pageSize: +e.target.value })}>
            {[10, 20, 30, 60, 100].map((n) => <option key={n} value={n}>{n} 条/页</option>)}
          </select>
        </div>
      </section>

      {menu && (
        <div className="ctx-menu" style={{ left: menu.x, top: menu.y }} onClick={(e) => e.stopPropagation()}>
          <div className="ctx-item" onClick={() => { goDetail(menu.gid); setMenu(null) }}>查看详情</div>
        </div>
      )}
    </div>
  )
}

function Select({ label, opts, value, onChange }) {
  return (
    <div className="filter-group">
      <label>{label}</label>
      <select className="select" value={value || ''} onChange={(e) => onChange(e.target.value)}>
        <option value="">全部</option>
        {(opts || []).map((o) => <option key={o} value={o}>{o}</option>)}
      </select>
    </div>
  )
}
