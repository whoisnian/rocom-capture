import React, { useState, useEffect, useContext, useMemo } from 'react'
import { getEvents, getEventCount, clearEvents, subscribe, getMedals } from '../api'
import { Avatar, Marks, Blood, locTag, fmtTime } from '../components/bits'
import { PetDetailModal } from './PetDetail'
import { AccountContext } from '../App'

// 事件流里值得单独标出的稀有血脉(元素系血脉几乎人人有,不展示以免刷屏)
const NOTABLE_BLOODS = ['污染', '奇异']

const FIELDS = [
  { k: 'species', label: '种类' },
  { k: 'nature', label: '性格' },
  { k: 'medal', label: '奖牌' },
  { k: 'type', label: '系别' },
]

// 异色/炫彩始终高亮(不再作为可配规则),读取时顺手剔除历史遗留的这两类规则。
function loadRules() {
  try {
    const r = JSON.parse(localStorage.getItem('hlRules') || '[]')
    return Array.isArray(r) ? r.filter((x) => x.field !== 'shiny' && x.field !== 'colorful') : []
  } catch { return [] }
}

// ownedMedalNames 返回宠物【拥有】的奖牌名集合(medalIds 经 id→名映射;佩戴+custom+free)。
function ownedMedalNames(pet, medalById) {
  const s = new Set()
  for (const id of pet?.medalIds || []) { const n = medalById.get(id); if (n) s.add(n) }
  return s
}

function matchRule(pet, rule, ownedMedals) {
  if (!pet) return false
  if (rule.field === 'type') return (pet.types || []).includes(rule.value)
  if (rule.field === 'medal') return ownedMedals.has(rule.value) // 检查拥有的奖牌(非仅佩戴)
  return String(pet[rule.field] || '') === rule.value
}
// 异色/炫彩始终高亮;此外任一规则命中即高亮(规则内部为精确匹配)。
// ownedMedals=该宠物拥有的奖牌名集合。
function isHighlight(pet, rules, ownedMedals) {
  if (!pet) return false
  if (pet.shiny || pet.colorful) return true
  return rules.some((r) => matchRule(pet, r, ownedMedals))
}

export default function Events() {
  const account = useContext(AccountContext)
  const [events, setEvents] = useState([])
  // total=自上次清空以来累计获得的宠物数(即列表最新一条的序号);列表可能因上限被截断,
  // 故序号以后端总数为准:列表第 i 条(0=最新)序号 = total - i。
  const [total, setTotal] = useState(0)
  const [rules, setRules] = useState(loadRules)
  // 高亮规则编辑区折叠态:配好规则后收起,把纵向空间让给事件流(首次无规则时默认展开)
  const [rulesOpen, setRulesOpen] = useState(() => {
    const s = localStorage.getItem('hlOpen')
    return s == null ? loadRules().length === 0 : s === '1'
  })
  const [medals, setMedals] = useState([])
  const [draft, setDraft] = useState({ field: 'nature', value: '' })
  // 奖牌 id→名映射(供奖牌规则按「拥有」判定;宠物 medalIds 存的是 id)
  const medalById = useMemo(() => new Map(medals.map((m) => [m.id, m.name])), [medals])
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
  useEffect(() => { localStorage.setItem('hlOpen', rulesOpen ? '1' : '0') }, [rulesOpen])
  useEffect(() => { localStorage.setItem('onlyHl', onlyHl ? '1' : '0') }, [onlyHl])
  useEffect(() => { getMedals().then((m) => setMedals(m || [])).catch(() => {}) }, [])

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

  const addRule = () => {
    const value = draft.value.trim()
    if (!value) return
    setRules((r) => [...r, { field: draft.field, value }])
    setDraft({ field: draft.field, value: '' })
  }
  const delRule = (i) => setRules((r) => r.filter((_, idx) => idx !== i))
  // 清空事件历史(后端删除 + 前端清列表并将计数归零,下次获得从 1 重新计)
  const clearAll = () => {
    if (!window.confirm('确定清空所有事件历史?计数将从头开始。')) return
    clearEvents().then(() => { setEvents([]); setTotal(0) }).catch(() => {})
  }

  return (
    <div>
      <div className="rules">
        <div className="rules-head" onClick={() => setRulesOpen((v) => !v)}>
          <h3>高亮规则</h3>
          <button className="btn small" onClick={(e) => { e.stopPropagation(); setRulesOpen((v) => !v) }}>
            {rulesOpen ? '收起 ▲' : `编辑${rules.length ? ` (${rules.length})` : ''} ▼`}
          </button>
        </div>
        {rulesOpen && (
          <div className="muted small">满足任一规则的事件将高亮，异色/炫彩始终高亮</div>
        )}
        {rulesOpen && (
          <div className="rule-row">
            <select className="select" style={{ width: 120 }} value={draft.field} onChange={(e) => setDraft({ field: e.target.value, value: '' })}>
              {FIELDS.map((f) => <option key={f.k} value={f.k}>{f.label}</option>)}
            </select>
            <input className="input" style={{ maxWidth: 200 }} placeholder="例如 固执 / 大块头 / 火"
              value={draft.value} onChange={(e) => setDraft((d) => ({ ...d, value: e.target.value }))}
              onKeyDown={(e) => e.key === 'Enter' && addRule()} />
            <button className="btn primary" onClick={addRule}>添加</button>
          </div>
        )}
        {(rules.length > 0 || rulesOpen) && (
          <div className="chips">
            {rules.map((r, i) => (
              <span key={i} className="chip on" onClick={() => delRule(i)}>
                {FIELDS.find((f) => f.k === r.field)?.label}: {r.value} ✕
              </span>
            ))}
            {rules.length === 0 && <span className="muted">未设置规则</span>}
          </div>
        )}
      </div>

      <div className="event-head">
        <h3>实时事件 <span className="muted" style={{ fontWeight: 400, fontSize: 13 }}>共 {total} 只</span></h3>
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
          .map((ev, i) => ({ ev, i, hl: isHighlight(ev.pet, rules, ownedMedalNames(ev.pet, medalById)) }))
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
                <div className="pet-sub">Lv.{ev.pet?.level} · {ev.pet?.nature} · {ev.pet?.medal || '无奖牌'} · {locTag(ev.pet)}</div>
              </div>
            </div>
          ))}
        {events.length === 0 && <div className="empty">暂无事件。游戏中捕捉/孵蛋新宠物后将实时出现在这里。</div>}
        {events.length > 0 && onlyHl && !events.some((ev) => isHighlight(ev.pet, rules, ownedMedalNames(ev.pet, medalById))) &&
          <div className="empty">当前没有命中高亮规则的事件。{rules.length === 0 ? '请先添加高亮规则。' : ''}</div>}
      </div>

      {detailGid != null && <PetDetailModal gid={detailGid} onClose={() => setDetailGid(null)} />}
    </div>
  )
}
