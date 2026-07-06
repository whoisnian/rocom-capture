import React, { useState, useEffect, useCallback, useRef, useMemo, useContext } from 'react'
import { getPets, getFilterOptions, getBoxes, getTeams, getPetPage, subscribe, ALL_TYPES, ALL_EGG_GROUPS } from '../api'
import { AccountContext, IconsContext } from '../App'
import { Types, Six, Marks, Gender, Form, Blood, EggGroups, Avatar, StatRange, InlineIcon, locTag, fmtTime } from '../components/bits'
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

// 极值高亮阈值:声音 |v|>=96(接近 ±100 极值);体重百分位 <=2% 或 >=98%(接近该形态上下限)。
const voiceHot = (v) => Math.abs(v) >= 96
const pctHot = (pct) => pct != null && (pct <= 2 || pct >= 98)

// 捕捉时间区间选项(键存入 filter,查询时按本地时间实时算出 catch_time 下限,避免持久化的时间戳过期)。
const CATCH_RANGES = [
  ['', '全部'], ['h1', '最近一小时'], ['h6', '最近六小时'],
  ['today', '今日'], ['week', '本周'], ['month', '本月'],
]
// catchAfterTs 把区间键转为 unix 秒下限(0=不限);今日/本周/本月按本地日历边界(周一为一周起点)。
function catchAfterTs(range) {
  const nowSec = Math.floor(Date.now() / 1000)
  const startOfDay = () => { const d = new Date(); d.setHours(0, 0, 0, 0); return Math.floor(d.getTime() / 1000) }
  switch (range) {
    case 'h1': return nowSec - 3600
    case 'h6': return nowSec - 6 * 3600
    case 'today': return startOfDay()
    case 'week': { const d = new Date(); const back = (d.getDay() + 6) % 7; d.setDate(d.getDate() - back); d.setHours(0, 0, 0, 0); return Math.floor(d.getTime() / 1000) }
    case 'month': { const d = new Date(); const m = new Date(d.getFullYear(), d.getMonth(), 1); return Math.floor(m.getTime() / 1000) }
    default: return 0
  }
}
// withCatch 把 filter.catchRange 转成后端 catchAfter 时间戳(并从查询参数里去掉 catchRange)。
function withCatch(f) {
  const { catchRange, ...rest } = f
  const ts = catchAfterTs(catchRange)
  return ts > 0 ? { ...rest, catchAfter: ts } : rest
}

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
  const account = useContext(AccountContext)
  const icons = useContext(IconsContext)
  const [filter, setFilter] = useState(loadFilter)
  const [detailGid, setDetailGid] = useState(null) // 详情弹窗的 gid(null=关闭)
  const [data, setData] = useState({ total: 0, pets: [] })
  const [options, setOptions] = useState({})
  const [collapsed, setCollapsed] = useState(() => sessionStorage.getItem('petListCollapsed') !== '0')
  const [sync, setSync] = useState(() => localStorage.getItem('petSync') !== '0') // 实时同步:游戏内操作自动跳转到对应宠物(默认开)
  const [selected, setSelected] = useState(null) // 单击选中的 gid
  const [menu, setMenu] = useState(null)          // 右键/长按菜单 {gid,x,y}
  const [boxes, setBoxes] = useState([])          // 各盒子槽位布局
  const [teams, setTeams] = useState({ slots: [] }) // 大世界三队 18 格
  const [activeIdx, setActiveIdx] = useState(0)   // 示意图当前容器下标(0=队伍)
  const reloadRef = useRef(null)
  const filterRef = useRef(filter)      // 供 SSE 回调读取最新筛选(避免闭包旧值)
  const containersRef = useRef([])       // 供 SSE 回调按盒号查容器名(避免闭包旧值)
  const lpRef = useRef(null)        // 长按定时器
  const lpFiredRef = useRef(false)  // 本次触摸是否已触发长按
  const menuAtRef = useRef(0)       // 菜单打开时刻(用于忽略紧随的合成 click)
  const syncRef = useRef(sync)      // 供 SSE 回调读取最新同步开关(避免闭包旧值)

  const load = useCallback(() => { getPets(withCatch(filter)).then(setData).catch(() => {}) }, [filter])
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
  useEffect(() => { containersRef.current = containers }, [containers])
  useEffect(() => { filterRef.current = filter }, [filter])
  useEffect(() => { sessionStorage.setItem('petListFilter', JSON.stringify(filter)) }, [filter])
  useEffect(() => { sessionStorage.setItem('petListCollapsed', collapsed ? '1' : '0') }, [collapsed])
  useEffect(() => { syncRef.current = sync }, [sync])
  useEffect(() => { localStorage.setItem('petSync', sync ? '1' : '0') }, [sync])

  // 实时：收到宠物更新时防抖重载当前页;若带 focusGid(客户端刚调整位置),
  // 自动切到该宠物所在页并选中,示意图跟随展示其盒子/队伍。
  useEffect(() => {
    return subscribe((m) => {
      if (m.type !== 'pet') return
      if (m.account && m.account !== account) return // 只认当前账号的更新
      // 同步关闭时不自动跳转,避免打断当前筛选(仍走下方防抖刷新,列表静默更新)
      const focus = m.data && m.data.focusGid
      if (focus && syncRef.current) {
        setSelected(focus)
        // 清掉其它筛选、改按该宠物移动后所在的盒子过滤:既保证被选中的宠物一定在列表中
        // (否则原有筛选可能把它排除),又通过 filter.box 联动让左上角示意图切到该盒。
        const f = filterRef.current
        const base = { pageSize: f.pageSize, sort: f.sort, order: f.order }
        const box = m.data.focusBox
        if (box) {
          const cont = containersRef.current.find((c) => c.type === 'box' && c.id === box)
          base.box = cont ? `${cont.id}-${cont.name}` : `${box}-`
        }
        getPetPage(focus, base)
          .then((r) => setFilter({ ...base, page: (r && r.page) || 1 }))
          .catch(() => setFilter({ ...base, page: 1 }))
        loadBoxes()
      }
      // 防抖重载用 filterRef 读取最新筛选(含 focus 切过去的新页),
      // 避免捕获旧 load 闭包,在 600ms 后把列表拉回切换前的页。
      clearTimeout(reloadRef.current)
      reloadRef.current = setTimeout(() => {
        if (reloadRef.current) { getPets(withCatch(filterRef.current)).then(setData).catch(() => {}); loadBoxes() }
      }, 600)
    })
  }, [load, loadBoxes, account])

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
  // 盒位/队位均缺失:多为刚捕捉、登录快照之后新增的宠物。游戏「打开盒子」不重传布局,
  // 位置要等下次登录 / 挪格 / 整理才会经流量落库,故此处标「位置待同步」而非留空。
  const boxTag = (p) => ` · ${locTag(p)}`
  // 移动卡片用的紧凑位置标签(与桌面同一 locTag 权威格式,统一带「大世界」)
  const boxTagShort = (p) => locTag(p)

  return (
    <div className="list-layout">
      {/* 移动端筛选抽屉的背景遮罩:点击关闭 */}
      <div className={'filters-backdrop' + (collapsed ? '' : ' show')} onClick={() => setCollapsed(true)} />
      <aside className={'filters' + (collapsed ? ' collapsed' : '')}>
        {/* 抽屉标题栏(仅移动端显示):关闭入口与打开处的「筛选」按钮同侧 */}
        <div className="filters-bar">
          <span className="filters-title">筛选</span>
          <button className="icon-btn" onClick={() => setCollapsed(true)} aria-label="关闭筛选">✕</button>
        </div>
        <BoxMap
          container={active} selected={selected} onCell={onCell}
          onPrev={() => setActiveIdx((i) => (i - 1 + containers.length) % containers.length)}
          onNext={() => setActiveIdx((i) => (i + 1) % containers.length)}
        />
        <div className="filter-group filter-reset">
          <button className="btn" onClick={reset}>重置筛选</button>
        </div>
        <div className="filter-group">
          <label>系别</label>
          <div className="chips">
            {ALL_TYPES.map((t) => (
              <span key={t} className={'chip' + ((filter.types || []).includes(t) ? ' on' : '')} onClick={() => toggleType(t)}>
                <InlineIcon src={icons.type && icons.type[t]} className="chip-ic" alt="" />{t}
              </span>
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
        <Select label="蛋组" opts={ALL_EGG_GROUPS} value={filter.eggGroup} onChange={(v) => set({ eggGroup: v })} />
        <Select label="奖牌" opts={options.medal} value={filter.medal} onChange={(v) => set({ medal: v })} />
        <Select label="宠物盒" opts={options.box} value={filter.box} onChange={(v) => set({ box: v })} />
        <div className="filter-group">
          <label>捕捉时间</label>
          <select className="select" value={filter.catchRange || ''} onChange={(e) => set({ catchRange: e.target.value })}>
            {CATCH_RANGES.map(([v, lbl]) => <option key={v || 'all'} value={v}>{lbl}</option>)}
          </select>
        </div>
        <div className="filter-group">
          <label>性别</label>
          <div className="radios">
            {['', '♂', '♀'].map((v) => (
              <label key={v || 'all'} className="radio">
                <input type="radio" name="gender" checked={(filter.gender || '') === v} onChange={() => set({ gender: v })} />
                {v ? <Gender g={v} /> : '全部'}
              </label>
            ))}
          </div>
        </div>
        <div className="filter-group">
          <label>变异</label>
          <div className="checks">
            <label className="check">
              <input type="checkbox" checked={filter.shiny === '1'} onChange={(e) => set({ shiny: e.target.checked ? '1' : '' })} />异色
            </label>
            <label className="check">
              <input type="checkbox" checked={filter.colorful === '1'} onChange={(e) => set({ colorful: e.target.checked ? '1' : '' })} />炫彩
            </label>
          </div>
        </div>
        {/* 抽屉底部操作条(仅移动端显示):重置 + 查看结果并关闭 */}
        <div className="filters-foot">
          <button className="btn" onClick={reset}>重置</button>
          <button className="btn primary" onClick={() => setCollapsed(true)}>查看 {data.total} 只</button>
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
          <button className={'btn' + (sync ? ' primary' : '')} title="开启后,游戏内捕捉/移动宠物会自动跳转并选中该宠物;关闭可避免打断当前筛选" onClick={() => setSync((v) => !v)}>同步</button>
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
                        <div className="pet-name">{p.name || p.species}<Gender g={p.gender} /><Marks p={p} /><Blood p={p} iconOnly /><Form form={p.form} /><EggGroups groups={p.eggGroups} /></div>
                        <div className="pet-sub">{p.species} · Lv.{p.level}{p.book ? ` · #${p.book}` : ""}{boxTag(p)}</div>
                      </div>
                    </div>
                  </td>
                  <td><Types types={p.types} icons={p.typeIcons} plain /></td>
                  <td>{p.nature || '-'}</td>
                  <td>{p.speciality || '无'}</td>
                  <td>{p.medal || '-'}</td>
                  <td className={voiceHot(p.voice) ? 'val-hot' : undefined}>{p.voice}</td>
                  <td className={pctHot(p.weightPct) ? 'val-hot' : undefined}><StatRange value={p.weightKg} min={p.weightMin} max={p.weightMax} pct={p.weightPct} unit=" kg" /></td>
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
                  <div className="pet-name">{p.name || p.species}<Gender g={p.gender} /><Marks p={p} /><Blood p={p} iconOnly /><Form form={p.form} /></div>
                  <div className="pet-sub">
                    {p.species} · Lv.{p.level}
                    {boxTagShort(p) && <> · <span className="loc">{boxTagShort(p)}</span></>}
                  </div>
                </div>
                <Types types={p.types} icons={p.typeIcons} plain />
              </div>
              <div className="card-grid">
                <div>性格：{p.nature || '-'}</div>
                <div>特长：{p.speciality || '无'}</div>
                <div>奖牌：{p.medal || '-'}</div>
                {p.eggGroups?.length > 0 && <div className="egg-cell">蛋组：<EggGroups groups={p.eggGroups} /></div>}
                <div>体重：<span className={pctHot(p.weightPct) ? 'val-hot' : undefined}><StatRange value={p.weightKg} min={p.weightMin} max={p.weightMax} pct={p.weightPct} unit=" kg" /></span></div>
                <div>身高：<StatRange value={p.heightM} min={p.heightMin} max={p.heightMax} pct={p.heightPct} unit=" m" /></div>
                <div>声音：<span className={voiceHot(p.voice) ? 'val-hot' : undefined}>{p.voice}</span></div>
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
          <select className="select pager-size" value={filter.pageSize} onChange={(e) => set({ pageSize: +e.target.value })}>
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
