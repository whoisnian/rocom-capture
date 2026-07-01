import React, { useState, useEffect, useCallback, useRef, useMemo } from 'react'
import { getPets, getFilterOptions, getBoxes, getTeams, getPetPage, subscribe, ALL_TYPES } from '../api'
import { Types, Six, Marks, Gender, Form, Avatar, StatRange, boxLabel, teamLabel, fmtTime } from '../components/bits'
import { PetDetailModal } from './PetDetail'

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
  { key: 'boxpos', label: '盒子位置' },
  { key: 'level', label: '等级' },
  { key: 'weight', label: '体重' },
  { key: 'height', label: '身高' },
  { key: 'voice', label: '声音' },
  { key: 'catchTime', label: '捕捉时间' },
]

// 列表状态(筛选/排序/分页/滚动)用 sessionStorage 持久化,从详情返回时还原。
const DEFAULT_FILTER = { page: 1, pageSize: 20, sort: 'boxpos', order: 'asc' }
function loadFilter() {
  try {
    const s = JSON.parse(sessionStorage.getItem('petListFilter'))
    if (s && typeof s === 'object') return s
  } catch { /* ignore */ }
  return DEFAULT_FILTER
}

export default function PetList() {
  const [filter, setFilter] = useState(loadFilter)
  const [detailGid, setDetailGid] = useState(null) // 详情弹窗的 gid(null=关闭)
  const [data, setData] = useState({ total: 0, pets: [] })
  const [options, setOptions] = useState({})
  const [collapsed, setCollapsed] = useState(() => sessionStorage.getItem('petListCollapsed') !== '0')
  const [selected, setSelected] = useState(null) // 单击选中的 gid
  const [menu, setMenu] = useState(null)          // 右键/长按菜单 {gid,x,y}
  const [boxes, setBoxes] = useState([])          // 各盒子槽位布局
  const [teams, setTeams] = useState({ slots: [] }) // 大世界三队 18 格
  const [activeIdx, setActiveIdx] = useState(0)   // 示意图当前容器下标(0=队伍)
  const reloadRef = useRef(null)
  const filterRef = useRef(filter)  // 供 SSE 回调读取最新筛选(避免闭包旧值)
  const lpRef = useRef(null)        // 长按定时器
  const lpFiredRef = useRef(false)  // 本次触摸是否已触发长按
  const menuAtRef = useRef(0)       // 菜单打开时刻(用于忽略紧随的合成 click)

  const load = useCallback(() => { getPets(filter).then(setData).catch(() => {}) }, [filter])
  const loadBoxes = useCallback(() => {
    getBoxes().then(setBoxes).catch(() => {})
    getTeams().then(setTeams).catch(() => {})
  }, [])
  useEffect(() => { load() }, [load])
  useEffect(() => { getFilterOptions().then(setOptions).catch(() => {}) }, [])
  useEffect(() => { loadBoxes() }, [loadBoxes])

  // 示意图容器:大世界队伍(6 排 × 3 队,竖向)排在所有盒子前,其后各盒子(5 排 × 6 格)
  const containers = useMemo(() => {
    // 原始 18 格为队序(team*6+pos);转置为「行=位置、列=队伍」的显示序(pos*3+team)
    const raw = teams.slots && teams.slots.length ? teams.slots : new Array(18).fill(0)
    const teamDisplay = []
    for (let pos = 0; pos < 6; pos++) for (let t = 0; t < 3; t++) teamDisplay.push(raw[t * 6 + pos])
    const list = [{ type: 'team', name: '大世界队伍', cols: 3, slots: teamDisplay, heads: teams.heads || {} }]
    for (const b of boxes) list.push({ type: 'box', id: b.id, name: b.name || ('盒' + b.id), cols: 6, slots: b.slots, heads: b.heads || {} })
    return list
  }, [teams, boxes])
  const boxIdxById = (id) => containers.findIndex((c) => c.type === 'box' && c.id === id)
  // 宠物盒筛选变化时,示意图跟随展示该盒
  useEffect(() => {
    const id = parseInt((filter.box || '').split('-')[0], 10)
    if (id) { const i = boxIdxById(id); if (i >= 0) setActiveIdx(i) }
  }, [filter.box, containers])

  // 持久化筛选状态与筛选栏折叠态
  useEffect(() => { filterRef.current = filter }, [filter])
  useEffect(() => { sessionStorage.setItem('petListFilter', JSON.stringify(filter)) }, [filter])
  useEffect(() => { sessionStorage.setItem('petListCollapsed', collapsed ? '1' : '0') }, [collapsed])

  // 实时：收到宠物更新时防抖重载当前页;若带 focusGid(手机端刚调整位置),
  // 自动切到该宠物所在页并选中,示意图跟随展示其盒子/队伍。
  useEffect(() => {
    return subscribe((m) => {
      if (m.type !== 'pet') return
      const focus = m.data && m.data.focusGid
      if (focus) {
        getPetPage(focus, filterRef.current)
          .then((r) => setFilter((f) => ({ ...f, page: (r && r.page) || 1 })))
          .catch(() => {})
        setSelected(focus)
        loadBoxes()
      }
      // 防抖重载用 filterRef 读取最新筛选(含 focus 切过去的新页),
      // 避免捕获旧 load 闭包,在 600ms 后把列表拉回切换前的页。
      clearTimeout(reloadRef.current)
      reloadRef.current = setTimeout(() => {
        if (reloadRef.current) { getPets(filterRef.current).then(setData).catch(() => {}); loadBoxes() }
      }, 600)
    })
  }, [load, loadBoxes])

  const set = (patch) => setFilter((f) => ({ ...f, ...patch, page: patch.page || 1 }))
  const toggleType = (t) =>
    setFilter((f) => {
      const s = new Set(f.types || [])
      s.has(t) ? s.delete(t) : s.add(t)
      return { ...f, types: [...s], page: 1 }
    })
  const sortBy = (key) =>
    setFilter((f) => ({ ...f, sort: key, order: f.sort === key && f.order === 'asc' ? 'desc' : 'asc', page: 1 }))
  // 打开详情弹窗(不离开列表,保留当前操作状态);复制编号到剪贴板
  const openDetail = (gid) => { setSelected(gid); setDetailGid(gid); setMenu(null) }
  const copyGid = (gid) => {
    try { navigator.clipboard && navigator.clipboard.writeText(String(gid)) } catch { /* ignore */ }
    setMenu(null)
  }
  // 重置:清空所有过滤条件,保留排序与每页档位
  const reset = () => setFilter((f) => ({ page: 1, pageSize: f.pageSize, sort: f.sort, order: f.order }))

  // 右键/长按菜单:选中并在 (x,y) 弹出(限制不溢出视口),菜单内带上宠物用于"筛选相同…"
  const openMenu = (p, x, y) => {
    setSelected(p.gid)
    menuAtRef.current = Date.now()
    setMenu({ gid: p.gid, pet: p, x: Math.min(x, window.innerWidth - 140), y: Math.min(y, window.innerHeight - 180) })
  }
  // 应用一项筛选并关闭菜单(set 会把页码重置为 1)
  const filterSame = (patch) => { set(patch); setMenu(null) }
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

  // 选中宠物:高亮 + 示意图跟随展示其盒子/队伍
  const selectPet = (p) => {
    setSelected(p.gid)
    if (p.team) setActiveIdx(0)
    else if (p.box) { const i = boxIdxById(p.box.boxId); if (i >= 0) setActiveIdx(i) }
  }
  // 点击示意图格子:选中该宠物,并跳到列表里它所在页(超过一页时切页)。
  // 清掉其它筛选条件(仅保留排序/每页档位),确保目标宠物一定在列表中,
  // 否则原有筛选可能把它排除导致跳转落空。盒子格→筛到该盒;队伍格→不限盒。
  const onCell = (gid, container) => {
    setSelected(gid)
    const cleared = { pageSize: filter.pageSize, sort: filter.sort, order: filter.order }
    const base = container.type === 'box'
      ? { ...cleared, box: `${container.id}-${container.name}` }
      : { ...cleared }
    getPetPage(gid, base)
      .then((r) => setFilter({ ...base, page: (r && r.page) || 1 }))
      .catch(() => setFilter({ ...base, page: 1 }))
  }

  // 列表项交互:单击选中、右键(桌面)/长按(移动)弹菜单
  const itemProps = (p) => ({
    onClick: () => { if (lpFiredRef.current) { lpFiredRef.current = false; return } selectPet(p) },
    onDoubleClick: () => openDetail(p.gid),
    onContextMenu: (e) => { e.preventDefault(); openMenu(p, e.clientX, e.clientY) },
    onTouchStart: (e) => {
      lpFiredRef.current = false
      const t = e.touches[0]
      lpRef.current = setTimeout(() => { lpFiredRef.current = true; openMenu(p, t.clientX, t.clientY) }, 450)
    },
    onTouchMove: () => clearTimeout(lpRef.current),
    onTouchEnd: () => clearTimeout(lpRef.current),
  })

  const active = containers[Math.min(activeIdx, containers.length - 1)]

  const pages = Math.max(1, Math.ceil(data.total / filter.pageSize))
  const arrow = (k) => (filter.sort === k ? (filter.order === 'asc' ? ' ▲' : ' ▼') : '')
  const boxTag = (p) => (p.box ? ` · 📦${boxLabel(p.box)}` : p.team ? ` · 🌍大世界${teamLabel(p.team)}` : '')

  return (
    <div className="list-layout">
      <aside className={'filters' + (collapsed ? ' collapsed' : '')}>
        <BoxMap
          container={active} selected={selected} onCell={onCell}
          onPrev={() => setActiveIdx((i) => (i - 1 + containers.length) % containers.length)}
          onNext={() => setActiveIdx((i) => (i + 1) % containers.length)}
        />
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
        <Select label="形态" opts={options.form} value={filter.form} onChange={(v) => set({ form: v })} />
        <Select label="宠物盒" opts={options.box} value={filter.box} onChange={(v) => set({ box: v })} />
        <div className="filter-group">
          <label>性别</label>
          <select className="select" value={filter.gender || ''} onChange={(e) => set({ gender: e.target.value })}>
            <option value="">全部</option><option value="♂">♂ 雄</option><option value="♀">♀ 雌</option>
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
                <tr key={p.gid} className={p.gid === selected ? 'selected' : ''} {...itemProps(p)}>
                  <td>
                    <div className="pet-cell">
                      <Avatar p={p} />
                      <div>
                        <div className="pet-name">{p.name || p.species} <Gender g={p.gender} /> <Marks p={p} /> <Form form={p.form} /></div>
                        <div className="pet-sub">{p.species} · Lv.{p.level}{p.book ? ` · No.${p.book}` : ""}{boxTag(p)}</div>
                      </div>
                    </div>
                  </td>
                  <td><Types types={p.types} /></td>
                  <td>{p.nature || '-'}</td>
                  <td>{p.speciality || '无'}</td>
                  <td>{p.medal || '-'}</td>
                  <td>{p.voice}</td>
                  <td><StatRange value={p.weightKg} min={p.weightMin} max={p.weightMax} pct={p.weightPct} unit=" kg" /></td>
                  <td><StatRange value={p.heightM} min={p.heightMin} max={p.heightMax} pct={p.heightPct} unit=" m" /></td>
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
            <div className={'card' + (p.gid === selected ? ' selected' : '')} key={p.gid} {...itemProps(p)}>
              <div className="card-head">
                <Avatar p={p} />
                <div style={{ flex: 1 }}>
                  <div className="pet-name">{p.name || p.species} <Gender g={p.gender} /> <Marks p={p} /> <Form form={p.form} /></div>
                  <div className="pet-sub">{p.species} · Lv.{p.level}{p.book ? ` · No.${p.book}` : ""}{boxTag(p)}</div>
                </div>
                <Types types={p.types} />
              </div>
              <div className="card-grid">
                <div>性格：{p.nature || '-'}</div>
                <div>特长：{p.speciality || '无'}</div>
                <div>奖牌：{p.medal || '-'}</div>
                <div>体重：<StatRange value={p.weightKg} min={p.weightMin} max={p.weightMax} pct={p.weightPct} unit=" kg" /></div>
                <div>身高：<StatRange value={p.heightM} min={p.heightMin} max={p.heightMax} pct={p.heightPct} unit=" m" /></div>
                <div>声音：{p.voice}</div>
              </div>
              <Six p={p} />
            </div>
          ))}
        </div>

        {data.pets.length === 0 && <div className="empty">没有匹配的宠物</div>}

        <div className="pager">
          <button className="btn" disabled={filter.page <= 1} onClick={() => set({ page: 1 })}>首页</button>
          <button className="btn" disabled={filter.page <= 1} onClick={() => set({ page: filter.page - 1 })}>上一页</button>
          <span className="muted">{filter.page} / {pages}</span>
          <button className="btn" disabled={filter.page >= pages} onClick={() => set({ page: filter.page + 1 })}>下一页</button>
          <button className="btn" disabled={filter.page >= pages} onClick={() => set({ page: pages })}>尾页</button>
          <select className="select" style={{ width: 110 }} value={filter.pageSize} onChange={(e) => set({ pageSize: +e.target.value })}>
            {[10, 20, 30, 60, 100].map((n) => <option key={n} value={n}>{n} 条/页</option>)}
          </select>
        </div>
      </section>

      {menu && (
        <div className="ctx-menu" style={{ left: menu.x, top: menu.y }} onClick={(e) => e.stopPropagation()}>
          <div className="ctx-item" onClick={() => openDetail(menu.gid)}>查看详情</div>
          <div className="ctx-item" onClick={() => copyGid(menu.gid)}>复制编号</div>
          <div className="ctx-sep" />
          <div className="ctx-item" onClick={() => filterSame({ search: menu.pet.species })}>筛选相同种类</div>
          <div className="ctx-item" onClick={() => filterSame({ nature: menu.pet.nature, natureExclude: '' })}>筛选相同性格</div>
          <div className="ctx-item" onClick={() => filterSame({ speciality: menu.pet.speciality })}>筛选相同特长</div>
        </div>
      )}

      {detailGid != null && <PetDetailModal gid={detailGid} onClose={() => setDetailGid(null)} />}
    </div>
  )
}

// BoxMap 位置示意图(每行 6 格;盒子 5 排、队伍 3 队;有宠物格显示头像,灰=空,选中高亮)。
// 标题右侧上一个/下一个按钮在容器间切换(大世界队伍排在所有盒子最前)。
function BoxMap({ container, selected, onCell, onPrev, onNext }) {
  const slots = (container && container.slots) || []
  const heads = (container && container.heads) || {}
  const cols = (container && container.cols) || 6
  const cellTitle = (i) => {
    if (!container) return ''
    // 队伍:列=队、行=位(cols=3);盒子:行=排、列=格(cols=6)
    if (container.type === 'team') return `第${(i % cols) + 1}队第${Math.floor(i / cols) + 1}位`
    return `第${Math.floor(i / cols) + 1}排第${(i % cols) + 1}格`
  }
  return (
    <div className="boxmap">
      <div className="boxmap-head">
        <span className="boxmap-name">{container ? container.name : '盒子位置'}</span>
        <span className="boxmap-nav">
          <button className="boxmap-btn" title="上一个" onClick={onPrev}>‹</button>
          <button className="boxmap-btn" title="下一个" onClick={onNext}>›</button>
        </span>
      </div>
      <div className="boxmap-grid" style={{ gridTemplateColumns: `repeat(${cols}, 40px)` }}>
        {slots.map((gid, i) => (
          <div
            key={i}
            className={'boxmap-cell' + (gid ? ' filled' : '') + (gid && gid === selected ? ' on' : '')}
            title={gid ? cellTitle(i) : '空'}
            onClick={() => gid && onCell(gid, container)}
          >
            {gid && heads[gid] ? <img src={'/img/' + heads[gid]} alt="" loading="lazy" /> : null}
          </div>
        ))}
      </div>
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
