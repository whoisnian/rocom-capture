import React from 'react'

// Types 渲染系别色块。
export function Types({ types }) {
  return (
    <>
      {(types || []).map((t, i) => (
        <span key={i} className="type" data-t={t}>{t}</span>
      ))}
      {(!types || types.length === 0) && <span className="muted">-</span>}
    </>
  )
}

const SIX = [
  ['生命', 'hp'],
  ['物攻', 'attack'],
  ['物防', 'defense'],
  ['速度', 'speed'],
  ['魔攻', 'spAttack'],
  ['魔防', 'spDefense'],
]

// Six 渲染六维(性格 ±10% 升降箭头 + 天分等级 +N)。
export function Six({ p }) {
  return (
    <div className="six">
      {SIX.map(([label, key]) => {
        const s = p[key] || {}
        return (
          <div key={key}>
            {label} <b>{s.value ?? 0}</b>
            {s.nature === 1 && <span className="up" title="性格 +10%"> ↑</span>}
            {s.nature === -1 && <span className="down" title="性格 -10%"> ↓</span>}
            {s.talentLv > 0 && <span className="talent" title="天分等级">+{s.talentLv}</span>}
          </div>
        )
      })}
    </div>
  )
}

// Marks 渲染异色/炫彩标记。
export function Marks({ p }) {
  if (!p) return null
  return (
    <>
      {p.shiny && <span className="mark mark-shiny" title="异色">异</span>}
      {p.colorful && <span className="mark mark-colorful" title="炫彩">彩</span>}
    </>
  )
}

// fmtTime 把 unix 秒格式化为本地时间。
export function fmtTime(ts) {
  if (!ts) return '-'
  const d = new Date(ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
