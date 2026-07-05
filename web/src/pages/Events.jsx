import React, { useState, useEffect, useContext, useMemo } from 'react'
import { getEvents, getEventCount, clearEvents, subscribe, getMedals } from '../api'
import { Avatar, fmtTime } from '../components/bits'
import { PetDetailModal } from './PetDetail'
import { AccountContext } from '../App'

const FIELDS = [
  { k: 'species', label: '种类' },
  { k: 'nature', label: '性格' },
  { k: 'medal', label: '奖牌' },
  { k: 'type', label: '系别' },
  { k: 'shiny', label: '异色', fixed: '1' },
  { k: 'colorful', label: '炫彩', fixed: '1' },
]

function loadRules() {
  try { return JSON.parse(localStorage.getItem('hlRules') || '[]') } catch { return [] }
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
  if (rule.field === 'shiny') return !!pet.shiny
  if (rule.field === 'colorful') return !!pet.colorful
  if (rule.field === 'medal') return ownedMedals.has(rule.value) // 检查拥有的奖牌(非仅佩戴)
  return String(pet[rule.field] || '') === rule.value
}
// 任一规则命中即高亮(规则内部为精确匹配);ownedMedals=该宠物拥有的奖牌名集合。
function isHighlight(pet, rules, ownedMedals) {
  return rules.length > 0 && rules.some((r) => matchRule(pet, r, ownedMedals))
}

export default function Events() {
  const account = useContext(AccountContext)
  const [events, setEvents] = useState([])
  // total=自上次清空以来累计获得的宠物数(即列表最新一条的序号);列表可能因上限被截断,
  // 故序号以后端总数为准:列表第 i 条(0=最新)序号 = total - i。
  const [total, setTotal] = useState(0)
  const [rules, setRules] = useState(loadRules)
  const [medals, setMedals] = useState([])
  const [draft, setDraft] = useState({ field: 'nature', value: '' })
  // 奖牌 id→名映射(供奖牌规则按「拥有」判定;宠物 medalIds 存的是 id)
  const medalById = useMemo(() => new Map(medals.map((m) => [m.id, m.name])), [medals])
  const [detailGid, setDetailGid] = useState(null) // 详情弹窗的 gid(null=关闭)
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
    const field = draft.field
    const value = field === 'shiny' ? '1' : draft.value.trim()
    if (field !== 'shiny' && !value) return
    setRules((r) => [...r, { field, value }])
    setDraft({ field, value: '' })
  }
  const delRule = (i) => setRules((r) => r.filter((_, idx) => idx !== i))
  // 清空事件历史(后端删除 + 前端清列表并将计数归零,下次获得从 1 重新计)
  const clearAll = () => {
    if (!window.confirm('确定清空所有事件历史?计数将从头开始。')) return
    clearEvents().then(() => { setEvents([]); setTotal(0) }).catch(() => {})
  }

  return (
    <div>
      <h3>高亮规则 <span className="muted" style={{ fontWeight: 400, fontSize: 13 }}>满足任一规则的事件将高亮提醒</span></h3>
      <div className="rules">
        <div className="rule-row">
          <select className="select" style={{ width: 120 }} value={draft.field} onChange={(e) => setDraft({ field: e.target.value, value: '' })}>
            {FIELDS.map((f) => <option key={f.k} value={f.k}>{f.label}</option>)}
          </select>
          {draft.field !== 'shiny' && (
            <input className="input" style={{ maxWidth: 200 }} placeholder="例如 固执 / 大块头 / 火"
              value={draft.value} onChange={(e) => setDraft((d) => ({ ...d, value: e.target.value }))}
              onKeyDown={(e) => e.key === 'Enter' && addRule()} />
          )}
          <button className="btn primary" onClick={addRule}>添加</button>
        </div>
        <div className="chips">
          {rules.map((r, i) => (
            <span key={i} className="chip on" onClick={() => delRule(i)}>
              {FIELDS.find((f) => f.k === r.field)?.label}: {r.field === 'shiny' ? '是' : r.value} ✕
            </span>
          ))}
          {rules.length === 0 && <span className="muted">未设置规则</span>}
        </div>
      </div>

      <div className="event-head">
        <h3>实时事件 <span className="muted" style={{ fontWeight: 400, fontSize: 13 }}>累计获得 {total} 只</span></h3>
        <div className="spacer" />
        {'wakeLock' in navigator
          ? <button className={'btn' + (keepAwake ? ' primary' : '')} onClick={() => setKeepAwake((v) => !v)}
              title="阻止屏幕熄灭/变暗,方便盯着高亮提醒">{keepAwake ? '🔆 常亮中' : '屏幕常亮'}</button>
          : <button className="btn" disabled title="当前非 HTTPS/localhost 环境,浏览器不提供屏幕常亮">屏幕常亮</button>}
        <button className="btn" disabled={events.length === 0} onClick={clearAll}>清空</button>
      </div>
      <div className="event-list">
        {events.map((ev, i) => (
          <div key={ev.id || ev.gid + '-' + ev.time} className={'event' + (isHighlight(ev.pet, rules, ownedMedalNames(ev.pet, medalById)) ? ' hl' : '')}
            onClick={() => ev.gid && setDetailGid(ev.gid)}>
            <Avatar p={ev.pet} />
            <div className="event-body">
              <div className="event-row">
                <span className="event-seq muted">#{total - i}</span>
                <span className="pet-name">{ev.pet?.name || ev.pet?.species}</span>
                <span className="event-time muted">{fmtTime(ev.time)}</span>
              </div>
              <div className="pet-sub">{ev.pet?.species} · Lv.{ev.pet?.level} · {ev.pet?.nature} · {ev.pet?.medal || '无奖牌'}</div>
            </div>
          </div>
        ))}
        {events.length === 0 && <div className="empty">暂无事件。游戏中捕捉/孵蛋新宠物后将实时出现在这里。</div>}
      </div>

      {detailGid != null && <PetDetailModal gid={detailGid} onClose={() => setDetailGid(null)} />}
    </div>
  )
}
