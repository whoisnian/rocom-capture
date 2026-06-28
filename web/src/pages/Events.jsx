import React, { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { getEvents, subscribe } from '../api'
import { Types, Avatar, fmtTime } from '../components/bits'

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

function matchRule(pet, rule) {
  if (!pet) return false
  if (rule.field === 'type') return (pet.types || []).includes(rule.value)
  if (rule.field === 'shiny') return !!pet.shiny
  if (rule.field === 'colorful') return !!pet.colorful
  return String(pet[rule.field] || '') === rule.value
}
// 任一规则命中即高亮(规则内部为精确匹配)。
function isHighlight(pet, rules) {
  return rules.length > 0 && rules.some((r) => matchRule(pet, r))
}

export default function Events() {
  const nav = useNavigate()
  const [events, setEvents] = useState([])
  const [rules, setRules] = useState(loadRules)
  const [draft, setDraft] = useState({ field: 'nature', value: '' })

  useEffect(() => {
    getEvents({ limit: 100 }).then((e) => setEvents(e || [])).catch(() => {})
    return subscribe((m) => {
      if (m.type === 'event') setEvents((prev) => [m.data, ...prev].slice(0, 300))
    })
  }, [])

  useEffect(() => { localStorage.setItem('hlRules', JSON.stringify(rules)) }, [rules])

  const addRule = () => {
    const field = draft.field
    const value = field === 'shiny' ? '1' : draft.value.trim()
    if (field !== 'shiny' && !value) return
    setRules((r) => [...r, { field, value }])
    setDraft({ field, value: '' })
  }
  const delRule = (i) => setRules((r) => r.filter((_, idx) => idx !== i))

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

      <h3>实时事件</h3>
      <div className="event-list">
        {events.map((ev) => (
          <div key={ev.id || ev.gid + '-' + ev.time} className={'event' + (isHighlight(ev.pet, rules) ? ' hl' : '')}
            onClick={() => ev.gid && nav('/pets/' + ev.gid)}>
            <span className={'badge ' + ev.kind}>{ev.subKind || (ev.kind === 'obtain' ? '获得' : '失去')}</span>
            <Avatar p={ev.pet} />
            <div style={{ flex: 1 }}>
              <div className="pet-name">{ev.pet?.name || ev.pet?.species} <Types types={ev.pet?.types} /></div>
              <div className="pet-sub">{ev.pet?.species} · Lv.{ev.pet?.level} · {ev.pet?.nature} · {ev.pet?.medal || '无奖牌'}</div>
            </div>
            <span className="muted" style={{ fontSize: 12 }}>{fmtTime(ev.time)}</span>
          </div>
        ))}
        {events.length === 0 && <div className="empty">暂无事件。游戏中捕捉/孵蛋新宠物后将实时出现在这里。</div>}
      </div>
    </div>
  )
}
