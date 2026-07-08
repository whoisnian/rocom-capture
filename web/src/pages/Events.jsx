import React, { useState, useEffect, useContext } from 'react'
import { getEvents, getEventCount, clearEvents, subscribe, getNameOptions } from '../api'
import { Avatar, Marks, Blood, locTag, fmtTime, voiceHot, pctHot } from '../components/bits'
import { PetDetailModal } from './PetDetail'
import { HOT_NAMES } from './PetList'
import { AccountContext } from '../App'

// 事件流里值得单独标出的稀有血脉(元素系血脉几乎人人有,不展示以免刷屏)
const NOTABLE_BLOODS = ['污染', '奇异']

// 高亮规则维度。仅「种类」为自由输入(种类繁多且无全表点选),其余点选条目。
const FIELDS = [
  { k: 'species', label: '种类' },
  { k: 'nature', label: '性格' },
  { k: 'speciality', label: '特长' },
  { k: 'weight', label: '体重' },
  { k: 'voice', label: '声音' },
]
// 体重/声音按「在自身范围内的百分位」判定(非奖牌拥有):体重百分位 weightPct(0-100)、
// 声音 voice(-100~100)。阈值与宠物列表的极值高亮一致。大块头=体型最大、小不点=最小;
// 婉转声=声音最高昂、粗嗓门=最低沉(见奖牌定义)。
const WEIGHT_OPTS = ['大块头', '小不点']
const VOICE_OPTS = ['婉转声', '粗嗓门']

// 异色/炫彩始终高亮、系别与奖牌已废弃,读取时顺手剔除这些历史遗留规则。
function loadRules() {
  try {
    const r = JSON.parse(localStorage.getItem('hlRules') || '[]')
    const dropped = ['shiny', 'colorful', 'type', 'medal']
    return Array.isArray(r) ? r.filter((x) => !dropped.includes(x.field)) : []
  } catch { return [] }
}

function matchRule(pet, rule) {
  if (!pet) return false
  // 体重/声音按百分位实际判定,不依赖奖牌是否已获得。
  if (rule.field === 'weight') {
    const p = pet.weightPct // 0-100:接近上/下限即体型最大/最小
    return p != null && (rule.value === '大块头' ? p >= 98 : p <= 2)
  }
  if (rule.field === 'voice') {
    const v = pet.voice // -100~100:最高昂/最低沉
    return v != null && (rule.value === '婉转声' ? v >= 96 : v <= -96)
  }
  return String(pet[rule.field] || '') === rule.value
}
// 异色/炫彩始终高亮;此外按维度分组:同维度内任一条目命中即算该维度命中(OR),
// 维度之间按 mode 组合——'and'=每个维度都命中、'or'=任一维度命中(等价于任一规则命中)。
// 各维度均为单值(体重/声音取百分位区间),同维度取或可避免同选两极永不命中。
// 无规则时仅异色/炫彩高亮(避免 and 下 every([]) 恒真把全部点亮)。
function isHighlight(pet, rules, mode) {
  if (!pet) return false
  if (pet.shiny || pet.colorful) return true
  if (rules.length === 0) return false
  const groups = new Map() // field -> rules[]
  for (const r of rules) {
    if (!groups.has(r.field)) groups.set(r.field, [])
    groups.get(r.field).push(r)
  }
  const groupHit = (rs) => rs.some((r) => matchRule(pet, r))
  const g = [...groups.values()]
  return mode === 'or' ? g.some(groupHit) : g.every(groupHit)
}

export default function Events() {
  const account = useContext(AccountContext)
  const [events, setEvents] = useState([])
  // total=自上次清空以来累计获得的宠物数(即列表最新一条的序号);列表可能因上限被截断,
  // 故序号以后端总数为准:列表第 i 条(0=最新)序号 = total - i。
  const [total, setTotal] = useState(0)
  const [rules, setRules] = useState(loadRules)
  // 多规则联合逻辑:'and'=需全部命中(默认)、'or'=任一命中
  const [mode, setMode] = useState(() => localStorage.getItem('hlMode') === 'or' ? 'or' : 'and')
  // 高亮规则侧边栏:桌面常驻左栏,移动端为抽屉;collapsed 仅控制移动端抽屉开合(默认收起)
  const [collapsed, setCollapsed] = useState(() => sessionStorage.getItem('hlCollapsed') !== '0')
  const [nameOpts, setNameOpts] = useState({ speciality: [] }) // 全量特长点选条目
  const [speciesDraft, setSpeciesDraft] = useState('') // 「种类」自由输入框内容
  const [detailGid, setDetailGid] = useState(null) // 详情弹窗的 gid(null=关闭)
  // 仅展示命中高亮规则的事件,持久化到 localStorage
  const [onlyHl, setOnlyHl] = useState(() => localStorage.getItem('onlyHl') === '1')
  // 屏幕常亮开关(Screen Wake Lock),持久化到 localStorage
  const [keepAwake, setKeepAwake] = useState(() => localStorage.getItem('keepAwake') === '1')

  useEffect(() => {
    // 后端只记录获得宠物事件(放生/赠送出等减少事件不入库),故无需再按类型过滤。
    getEvents({ limit: 100 }).then((e) => setEvents(e || [])).catch(() => {})
    getEventCount().then((r) => setTotal(r?.count || 0)).catch(() => {})
    return subscribe((m) => {
      if (m.type !== 'event') return
      if (m.account && m.account !== account) return // 只认当前账号的事件
      setEvents((prev) => [m.data, ...prev].slice(0, 300))
      setTotal((n) => n + 1)
    })
  }, [account])

  useEffect(() => { localStorage.setItem('hlRules', JSON.stringify(rules)) }, [rules])
  useEffect(() => { localStorage.setItem('hlMode', mode) }, [mode])
  useEffect(() => { sessionStorage.setItem('hlCollapsed', collapsed ? '1' : '0') }, [collapsed])
  useEffect(() => { localStorage.setItem('onlyHl', onlyHl ? '1' : '0') }, [onlyHl])
  useEffect(() => { getNameOptions().then((o) => setNameOpts(o || { speciality: [] })).catch(() => {}) }, [])

  // 某维度下可点选的条目:性格取宠物列表常用项、特长取全表、体重/声音取两极标签。
  const paletteFor = (field) => {
    if (field === 'nature') return HOT_NAMES
    if (field === 'speciality') return (nameOpts.speciality || []).filter((v) => v !== '无') // 「无特长」不作高亮项
    if (field === 'weight') return WEIGHT_OPTS
    if (field === 'voice') return VOICE_OPTS
    return []
  }
  const hasRule = (field, value) => rules.some((r) => r.field === field && r.value === value)
  // 点选条目:已选则移除、未选则添加(即时生效,无需「添加」按钮)。
  const toggleRule = (field, value) => setRules((r) => hasRule(field, value)
    ? r.filter((x) => !(x.field === field && x.value === value))
    : [...r, { field, value }])

  // 请求屏幕常亮锁,阻止设备熄屏/降亮。仅 secure context(HTTPS/localhost)可用;
  // 切到后台锁会被系统自动释放,回到前台需重新获取(visibilitychange)。
  useEffect(() => {
    localStorage.setItem('keepAwake', keepAwake ? '1' : '0')
    if (!keepAwake || !('wakeLock' in navigator)) return
    let lock = null
    const acquire = async () => {
      try { lock = await navigator.wakeLock.request('screen') } catch { /* 拒绝/不可用则静默 */ }
    }
    const onVis = () => { if (document.visibilityState === 'visible') acquire() }
    acquire()
    document.addEventListener('visibilitychange', onVis)
    return () => {
      document.removeEventListener('visibilitychange', onVis)
      lock?.release().catch(() => {})
    }
  }, [keepAwake])

  // 种类为自由输入,回车/点「添加」落规则(去重)。
  const addSpecies = () => {
    const value = speciesDraft.trim()
    if (value && !hasRule('species', value)) setRules((r) => [...r, { field: 'species', value }])
    setSpeciesDraft('')
  }
  const speciesRules = rules.filter((r) => r.field === 'species')
  // 清空事件历史(后端删除 + 前端清列表并将计数归零,下次获得从 1 重新计)
  const clearAll = () => {
    if (!window.confirm('确定清空所有事件历史?计数将从头开始。')) return
    clearEvents().then(() => { setEvents([]); setTotal(0) }).catch(() => {})
  }

  return (
    <div className="list-layout">
      {/* 移动端规则抽屉的背景遮罩:点击关闭 */}
      <div className={'filters-backdrop' + (collapsed ? '' : ' show')} onClick={() => setCollapsed(true)} />
      <aside className={'filters' + (collapsed ? ' collapsed' : '')}>
        {/* 标题行:标题在左,AND/OR 切换靠右;✕ 关闭仅移动端抽屉显示 */}
        <div className="rules-header">
          <h3 className="rules-title">高亮规则</h3>
          <div className="rule-logic-toggle">
            <button className={'btn small' + (mode === 'and' ? ' primary' : '')} onClick={() => setMode('and')}>AND</button>
            <button className={'btn small' + (mode === 'or' ? ' primary' : '')} onClick={() => setMode('or')}>OR</button>
          </div>
          <button className="icon-btn rules-close" onClick={() => setCollapsed(true)} aria-label="关闭规则">✕</button>
        </div>
        <div className="rule-logic">
          <span className="muted small" title="AND:各维度都要命中(同维度内任一条目即可)。OR:任一条目命中即可。体重/声音按百分位判定。异色/炫彩始终高亮。">
            {mode === 'and' ? '同时满足所选条件' : '任一条件命中'}即高亮，异色/炫彩始终高亮
          </span>
        </div>
        <div className="rule-groups">
          {FIELDS.map((f) => (
            <div className="filter-group" key={f.k}>
              <label>{f.label}</label>
              {f.k === 'species' ? (
                <>
                  <div className="rule-species-add">
                    <input className="input" placeholder="输入种类名，如 鸭吉吉"
                      value={speciesDraft} onChange={(e) => setSpeciesDraft(e.target.value)}
                      onKeyDown={(e) => e.key === 'Enter' && addSpecies()} />
                    <button className="btn primary" onClick={addSpecies}>添加</button>
                  </div>
                  {speciesRules.length > 0 && (
                    <div className="chips">
                      {speciesRules.map((r) => (
                        <span key={r.value} className="chip on" onClick={() => toggleRule('species', r.value)}>{r.value} ✕</span>
                      ))}
                    </div>
                  )}
                </>
              ) : (
                <div className="chips">
                  {paletteFor(f.k).map((v) => (
                    <span key={v} className={'chip' + (hasRule(f.k, v) ? ' on' : '')} onClick={() => toggleRule(f.k, v)}>{v}</span>
                  ))}
                  {paletteFor(f.k).length === 0 && <span className="muted">暂无可选条目</span>}
                </div>
              )}
            </div>
          ))}
        </div>
      </aside>

      <section>
        <div className="toolbar list-toolbar event-head">
          <button className="btn filter-toggle" onClick={() => setCollapsed(false)}>规则{rules.length ? ` (${rules.length})` : ''}</button>
          <strong className="event-title">实时事件</strong>
          <span className="muted">共 {total} 只</span>
          <div className="spacer" />
          {/* 三个操作统一为单图标,含义见各自 title */}
          <button className={'btn btn-icon' + (onlyHl ? ' primary' : '')} onClick={() => setOnlyHl((v) => !v)}
            title="仅展示命中高亮规则的事件">{onlyHl ? '★' : '☆'}</button>
          {'wakeLock' in navigator
            ? <button className={'btn btn-icon' + (keepAwake ? ' primary' : '')} onClick={() => setKeepAwake((v) => !v)}
                title="阻止屏幕熄灭,方便盯着高亮提醒">☀</button>
            : <button className="btn btn-icon" disabled title="当前非 HTTPS/localhost 环境,浏览器不提供屏幕常亮">☀</button>}
          <button className="btn btn-icon" disabled={events.length === 0} onClick={clearAll} title="清空事件历史">🗑</button>
        </div>
        <div className="event-list">
        {/* 先按原始下标算序号(#total-i)与高亮,再按"仅看高亮"过滤,保证序号不因过滤错位 */}
        {events
          .map((ev, i) => ({ ev, i, hl: isHighlight(ev.pet, rules, mode) }))
          .filter(({ hl }) => !onlyHl || hl)
          .map(({ ev, i, hl }) => (
            <div key={ev.id || ev.gid + '-' + ev.time} className={'event' + (hl ? ' hl' : '')}
              onClick={() => ev.gid && setDetailGid(ev.gid)}>
              <Avatar p={ev.pet} />
              <div className="event-body">
                <div className="event-row">
                  <span className="event-seq muted">#{total - i}</span>
                  <span className="pet-name">
                    {ev.pet?.name || ev.pet?.species}
                    <Marks p={ev.pet} />
                    {NOTABLE_BLOODS.includes(ev.pet?.blood) && <Blood p={ev.pet} iconOnly />}
                  </span>
                  <span className="event-time muted">{fmtTime(ev.time)}</span>
                </div>
                <div className="pet-sub">
                  {ev.pet?.nature}
                  {ev.pet?.speciality && ev.pet.speciality !== '无' ? ` · ${ev.pet.speciality}` : ''}
                  {' · W '}<span className={pctHot(ev.pet?.weightPct)}>{ev.pet?.weightPct != null ? `${Math.round(ev.pet.weightPct)}%` : '-'}</span>
                  {' · V '}<span className={voiceHot(ev.pet?.voice)}>{ev.pet?.voice ?? '-'}</span>
                  {' · '}{locTag(ev.pet)}
                </div>
              </div>
            </div>
          ))}
          {events.length === 0 && <div className="empty">暂无事件。游戏中捕捉/孵蛋新宠物后将实时出现在这里。</div>}
          {events.length > 0 && onlyHl && !events.some((ev) => isHighlight(ev.pet, rules, mode)) &&
            <div className="empty">当前没有命中高亮规则的事件。{rules.length === 0 ? '请先添加高亮规则。' : ''}</div>}
        </div>
      </section>

      {detailGid != null && <PetDetailModal gid={detailGid} onClose={() => setDetailGid(null)} />}
    </div>
  )
}
